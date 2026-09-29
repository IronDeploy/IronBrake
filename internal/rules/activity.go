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
// sem diferença de espaços, aspas, barras e maiúsculas; com as opções em
// ordem alfabética; com cada sequência de dígitos trocada por "#"; e com o
// nome de arquivo trocado por um marcador (o plano do terraform apply, e
// qualquer argumento que termine em .tfplan, .yaml, .json, .sql...). Assim
// terraform apply plan1, plan2 e plan-a contam como o mesmo comando: um
// agente em loop costuma só trocar o nome do arquivo.
func Normalize(command string) string {
	var parts []string
	for _, tokens := range splitCommands(command) {
		parts = append(parts, normalizeTokens(tokens))
	}
	return digitRun.ReplaceAllString(strings.ToLower(strings.Join(parts, " ; ")), "#")
}

var (
	digitRun = regexp.MustCompile(`[0-9]+`)
	// fileLike: argumento que é o nome de um arquivo de plano, manifesto ou script.
	fileLike = regexp.MustCompile(`(?i)\.(?:tfplan|plan|ya?ml|json|sql|tf|tfvars|hcl|sh|txt)$`)
)

func normalizeTokens(tokens []string) string {
	sub, _, _ := terraformSubcommand(tokens)
	var flags, rest []string
	afterDoubleDash, seenSub := false, false
	for _, t := range tokens[1:] {
		switch {
		case afterDoubleDash:
			rest = append(rest, normalizeFile(t))
		case t == "--":
			afterDoubleDash = true
			rest = append(rest, t)
		case strings.HasPrefix(t, "-") && t != "-":
			flags = append(flags, normalizeFlag(t))
		case sub == "apply" && seenSub: // o argumento do apply é o plano
			rest = append(rest, "<plan>")
		default:
			if t == sub {
				seenSub = true
			}
			rest = append(rest, normalizeFile(t))
		}
	}
	slices.Sort(flags)
	return strings.Join(append(append([]string{tokens[0]}, rest...), flags...), " ")
}

func normalizeFile(t string) string {
	if fileLike.MatchString(t) {
		return "<file>"
	}
	return t
}

// normalizeFlag troca o valor colado (-out=x.tfplan, --filename=a.yaml).
func normalizeFlag(t string) string {
	if flag, value, found := strings.Cut(t, "="); found && fileLike.MatchString(value) {
		return flag + "=<file>"
	}
	return t
}

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
