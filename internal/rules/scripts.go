package rules

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// maxScriptDepth: quantos scripts um dentro do outro são seguidos.
const maxScriptDepth = 2

// scriptRule lê o arquivo que o comando manda executar (./deploy.sh, bash
// x.sh, python3 deploy.py, make deploy) e julga o conteúdo com as mesmas
// regras. Não executa nada: só lê, com o mesmo limite de tamanho das outras
// leituras. Arquivo ilegível, remoto ou grande demais fica de fora (allow).
type scriptRule struct{}

var scriptContent Rule = scriptRule{}

func (scriptRule) Name() string { return "script-content" }

type scriptKind int

const (
	shellScript scriptKind = iota
	codeScript             // python, node, ruby, perl, php
	makeTargets
)

type scriptTarget struct {
	kind  scriptKind
	path  string   // o arquivo (Makefile, no caso do make)
	names []string // alvos pedidos ao make
}

func (scriptRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	if env.ReadFile == nil || env.scriptDepth >= maxScriptDepth {
		return hook.Allow, ""
	}
	for _, tokens := range commands {
		for _, target := range scriptTargets(tokens) {
			verdict := judgeScript(target, env)
			if verdict.Decision == hook.Allow {
				continue
			}
			reason := verdict.Reason + " (encontrado no arquivo " + filepath.Base(target.path) + ")"
			// O nome do script também conta: deploy-prod.sh.
			if verdict.Decision == hook.Ask && verdict.Reason != rmScriptEmptyVarReason && env.isProduction(commands) {
				danger := strings.TrimPrefix(verdict.Reason, "Iron Brake: confirme antes de executar: ")
				return hook.Deny, "Iron Brake: bloqueado em produção: o arquivo que o comando executa contém algo perigoso: " + danger
			}
			return verdict.Decision, reason
		}
	}
	return hook.Allow, ""
}

func judgeScript(target scriptTarget, env Env) Verdict {
	data, ok := env.readTargetFile(target.path)
	if !ok {
		return Verdict{Decision: hook.Allow}
	}
	content := string(data)

	inner := env
	inner.scriptDepth++
	inner.inScript = true
	inner.scriptNounset = nounsetRe.MatchString(content)
	inner.scriptAssigned = assignedVars(content)

	var commands [][]string
	switch target.kind {
	case shellScript:
		commands = split(stripShellComments(content), 1)
	case makeTargets:
		commands = split(makeRecipes(content, target.names), 1)
	case codeScript:
		// O código vira um "python3 -c CÓDIGO": as regras de SDK e os
		// comandos de shell que ele dispara são julgados como sempre.
		commands = [][]string{{"python3", "-c", content}}
		for _, command := range shellOuts(content) {
			commands = append(commands, split(command, 1)...)
		}
	}
	return runRules(commands, inner, registry)
}

var (
	shells       = set("sh", "bash", "zsh", "dash", "ksh")
	scriptExts   = map[string]scriptKind{".sh": shellScript, ".bash": shellScript, ".zsh": shellScript, ".py": codeScript, ".rb": codeScript, ".js": codeScript, ".mjs": codeScript, ".pl": codeScript, ".php": codeScript}
	makePrograms = set("make", "gmake")
)

// scriptTargets: os arquivos que este comando manda executar.
func scriptTargets(tokens []string) []scriptTarget {
	if len(tokens) == 0 {
		return nil
	}
	prog := programName(tokens[0])
	args := tokens[1:]

	switch {
	case shells[prog]:
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "-o" || args[i] == "+o":
				i++
			case strings.HasPrefix(args[i], "-") || strings.HasPrefix(args[i], "+"):
			default:
				return []scriptTarget{{kind: shellScript, path: args[i]}}
			}
		}
	case prog == "source" || prog == ".":
		if len(args) > 0 {
			return []scriptTarget{{kind: shellScript, path: args[0]}}
		}
	case makePrograms[prog]:
		return makeTarget(args)
	case interpreterFamily(tokens[0]) != "":
		for _, a := range args {
			if a == "-m" || a == "-c" || a == "-e" || a == "-r" || a == "--eval" {
				return nil // módulo ou código na linha: outra regra cuida
			}
		}
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				return []scriptTarget{{kind: codeScript, path: a}}
			}
		}
	case strings.ContainsRune(tokens[0], '/'):
		if kind, ok := scriptExts[strings.ToLower(filepath.Ext(tokens[0]))]; ok {
			return []scriptTarget{{kind: kind, path: tokens[0]}}
		}
	}
	return nil
}

// makeTarget lê -f ARQUIVO, -C PASTA e os alvos pedidos (vazio = o primeiro).
func makeTarget(args []string) []scriptTarget {
	file, dir := "Makefile", ""
	var names []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case (a == "-f" || a == "--file" || a == "--makefile") && i+1 < len(args):
			file = args[i+1]
			i++
		case (a == "-C" || a == "--directory") && i+1 < len(args):
			dir = args[i+1]
			i++
		case strings.HasPrefix(a, "-"):
		case strings.Contains(a, "="): // VAR=valor
		default:
			names = append(names, a)
		}
	}
	return []scriptTarget{{kind: makeTargets, path: filepath.Join(dir, file), names: names}}
}

var (
	nounsetRe    = regexp.MustCompile(`(?m)^\s*set\s+(?:-[a-zA-Z]*u|.*-o\s+nounset)`)
	assignedRe   = regexp.MustCompile(`(?m)(?:^|[;&|(]|\bexport\s+|\blocal\s+|\bdeclare\s+(?:-\w+\s+)?|\breadonly\s+)\s*([A-Za-z_][A-Za-z0-9_]*)=`)
	loopVarRe    = regexp.MustCompile(`(?m)\bfor\s+([A-Za-z_][A-Za-z0-9_]*)\s+in\b`)
	readVarRe    = regexp.MustCompile(`(?m)\bread\s+(?:-\w+\s+)*([A-Za-z_][A-Za-z0-9_]*)`)
	makeAssignRe = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*[:?+]?=`)
)

// assignedVars: os nomes que o script define. Não serve para achar o valor,
// só para saber que a variável não vem de fora.
func assignedVars(content string) map[string]bool {
	names := map[string]bool{}
	for _, re := range []*regexp.Regexp{assignedRe, loopVarRe, readVarRe, makeAssignRe} {
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			names[m[1]] = true
		}
	}
	return names
}

// stripShellComments tira as linhas que só têm comentário (e o #!).
func stripShellComments(content string) string {
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

var makeRule = regexp.MustCompile(`^([A-Za-z0-9_./%-][^:=\t]*?)\s*:(.*)$`)

const maxMakeTargets = 20

// makeRecipes devolve os comandos dos alvos pedidos (o primeiro alvo, se não
// houver pedido) e de suas dependências, como um script de shell.
func makeRecipes(makefile string, names []string) string {
	type target struct {
		prereqs []string
		recipe  []string
	}
	targets := map[string]*target{}
	var order []string
	var current *target
	for _, line := range strings.Split(makefile, "\n") {
		switch {
		case strings.HasPrefix(line, "\t"):
			if current != nil {
				current.recipe = append(current.recipe, strings.TrimLeft(strings.TrimSpace(line), "@-+"))
			}
		case strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#"):
		default:
			current = nil
			m := makeRule.FindStringSubmatch(line)
			if m == nil || strings.HasPrefix(m[2], "=") { // VAR := valor
				continue
			}
			prereqs := strings.Fields(strings.SplitN(m[2], ";", 2)[0])
			for _, name := range strings.Fields(m[1]) {
				if strings.HasPrefix(name, ".") {
					continue // .PHONY, .DEFAULT...
				}
				t := targets[name]
				if t == nil {
					t = &target{}
					targets[name] = t
					order = append(order, name)
				}
				t.prereqs = append(t.prereqs, prereqs...)
				current = t
			}
			// Comando na mesma linha do alvo: "deploy: ; kubectl apply -f x".
			if _, inline, found := strings.Cut(m[2], ";"); found && current != nil {
				current.recipe = append(current.recipe, strings.TrimSpace(inline))
			}
		}
	}
	if len(names) == 0 && len(order) > 0 {
		names = order[:1]
	}

	var out []string
	seen := map[string]bool{}
	for queue := names; len(queue) > 0 && len(seen) < maxMakeTargets; queue = queue[1:] {
		name := queue[0]
		t := targets[name]
		if seen[name] || t == nil {
			continue
		}
		seen[name] = true
		out = append(out, strings.ReplaceAll(strings.Join(t.recipe, "\n"), "$$", "$"))
		queue = append(queue, t.prereqs...)
	}
	return strings.Join(out, "\n")
}
