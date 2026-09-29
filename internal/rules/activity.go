package rules

import (
	"regexp"
	"slices"
	"strings"
)

// infraPrograms mexem em infraestrutura, assim como clientes SQL e git push.
var infraPrograms = []string{
	"terraform", "tofu", "terragrunt", "kubectl", "helm", "aws", "az", "gcloud", "gsutil",
}

// Normalize devolve a forma canônica da linha, para comparar repetições:
// sem diferença de espaços, aspas, barras e maiúsculas, e com cada sequência
// de dígitos trocada por "#". Assim terraform apply plan1 e plan2 (ou
// plan-20260929-1030) contam como o mesmo comando; um agente em loop costuma
// só trocar o número do arquivo.
func Normalize(command string) string {
	var parts []string
	for _, tokens := range splitCommands(command) {
		parts = append(parts, strings.Join(tokens, " "))
	}
	return digitRun.ReplaceAllString(strings.ToLower(strings.Join(parts, " ; ")), "#")
}

var digitRun = regexp.MustCompile(`[0-9]+`)

// TouchesInfra diz se algum comando da linha mexe em infraestrutura.
func TouchesInfra(command string) bool {
	commands := splitCommands(command)
	if callsSQLClient(commands) {
		return true
	}
	for _, tokens := range commands {
		if slices.Contains(infraPrograms, programName(tokens[0])) {
			return true
		}
		if sub, _ := gitSubcommand(tokens); sub == "push" {
			return true
		}
	}
	return false
}

// classSubcommands: só estes subcomandos entram na classe; texto livre ali
// poderia ser um segredo.
var classSubcommands = map[string][]string{
	"terraform": {"apply", "destroy", "plan", "init", "show", "state", "import", "output", "validate", "fmt", "workspace", "taint", "untaint", "refresh"},
	"git":       {"push", "reset", "clean", "status", "commit", "pull", "fetch", "checkout", "switch", "restore", "merge", "rebase", "log", "diff", "add", "branch", "stash", "tag", "clone"},
	"kubectl":   {"delete", "drain", "cordon", "uncordon", "get", "describe", "apply", "create", "edit", "patch", "scale", "rollout", "logs", "exec", "port-forward", "config"},
	"helm":      {"install", "upgrade", "uninstall", "rollback", "list", "status", "template"},
}

// Classify devolve a classe da linha para o log de auditoria, como
// "terraform apply" ou "outro, git push", sem nenhum argumento.
func Classify(command string) string {
	var classes []string
	for _, tokens := range splitCommands(command) {
		class := classifyCommand(tokens)
		if !slices.Contains(classes, class) {
			classes = append(classes, class)
		}
	}
	return strings.Join(classes, ", ")
}

func classifyCommand(tokens []string) string {
	if callsSQLClient([][]string{tokens}) {
		return "sql"
	}

	program := programName(tokens[0])
	var sub string
	switch program {
	case "terraform":
		sub, _, _ = terraformSubcommand(tokens)
	case "git":
		sub, _ = gitSubcommand(tokens)
	case "kubectl":
		if args := positionals(tokens[1:], kubectlValueFlags); len(args) > 0 {
			sub = args[0]
		}
	case "helm":
		if args := positionals(tokens[1:], nil); len(args) > 0 {
			sub = args[0]
		}
	case "tofu", "terragrunt", "aws", "az", "gcloud":
		return program
	default:
		return "outro"
	}

	if slices.Contains(classSubcommands[program], sub) {
		return program + " " + sub
	}
	return program
}
