package rules

import (
	"slices"
	"strings"
)

const (
	cdkDestroyDanger     = "cdk destroy apaga as stacks do CloudFormation e os recursos que elas criaram: serviços saem do ar e dados podem ser perdidos."
	cdkAutoApproveDanger = "cdk deploy --require-approval never aplica sem confirmar mudanças de segurança (IAM, security groups): é o equivalente do terraform apply -auto-approve."
	samDeleteDanger      = "sam delete apaga a stack do CloudFormation e os artefatos no S3: os recursos criados por ela somem."
	eksctlDeleteDanger   = "eksctl delete apaga o cluster EKS, os grupos de nós ou as contas de serviço: as cargas que rodam ali saem do ar."
	pulumiDestroyDanger  = "pulumi destroy apaga todos os recursos da stack."
	pulumiYesDanger      = "pulumi up --yes aplica sem esperar a confirmação do preview: é o equivalente do terraform apply -auto-approve."
	pulumiForceDanger    = "pulumi stack rm --force apaga a stack mesmo com recursos, e eles ficam sem dono no provedor."
	pulumiStateDanger    = "pulumi state delete tira o recurso do estado sem apagá-lo na nuvem: o Pulumi deixa de enxergar algo que continua existindo (e cobrando)."
	serverlessDanger     = "serverless remove apaga a stack do CloudFormation do serviço e tudo o que ela criou."
	cdktfDestroyDanger   = "cdktf destroy apaga todos os recursos da stack."
	cdktfAutoApprove     = "cdktf deploy --auto-approve aplica sem confirmar: é o equivalente do terraform apply -auto-approve."
)

var iacDestructive = dangerRule{name: "iac-destructive", match: matchIaC}

func matchIaC(tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	args := tokens[1:]

	switch iacProgram(tokens[0]) {
	case "npm", "pnpm", "yarn", "bun":
		// npm exec -- cdk destroy, pnpm dlx aws-cdk destroy, pnpm cdk destroy...
		if rest := packageRunnerCommand(args); len(rest) > 0 {
			return matchIaC(rest)
		}
	case "cdk":
		return matchCdk(args)
	case "sam":
		if verb(args, samValueFlags, samVerbs) == "delete" {
			return samDeleteDanger, true
		}
	case "eksctl":
		if verb(args, eksctlValueFlags, eksctlVerbs) == "delete" {
			return eksctlDeleteDanger, true
		}
	case "pulumi":
		return matchPulumi(args)
	case "serverless", "sls":
		if verb(args, serverlessValueFlags, serverlessVerbs) == "remove" {
			return serverlessDanger, true
		}
	case "cdktf":
		switch verb(args, nil, cdktfVerbs) {
		case "destroy":
			return cdktfDestroyDanger, true
		case "deploy":
			if flagOn(args, "--auto-approve", 0) {
				return cdktfAutoApprove, true
			}
		}
	}
	return "", false
}

var (
	cdkVerbs = set("list", "ls", "synthesize", "synth", "bootstrap", "deploy", "destroy", "diff", "metadata",
		"init", "context", "docs", "doc", "doctor", "acknowledge", "ack", "notices", "import", "watch",
		"rollback", "gc", "migrate", "refactor", "flags")
	cdkValueFlags = set("--app", "-a", "--context", "-c", "--plugin", "-p", "--profile", "--proxy",
		"--ca-bundle-path", "--role-arn", "-r", "--output", "-o", "--build", "--toolkit-stack-name",
		"--require-approval", "--outputs-file", "-O", "--parameters", "--tags", "-t",
		"--notification-arns", "--progress", "--concurrency", "--method", "--change-set-name")

	samVerbs = set("init", "build", "deploy", "delete", "local", "logs", "package", "publish", "sync",
		"traces", "validate", "pipeline", "list", "remote", "bootstrap", "docs")
	samValueFlags = set("--stack-name", "--region", "--profile", "--config-env", "--config-file",
		"--s3-bucket", "--s3-prefix", "--template", "--template-file", "-t", "--template-file")

	eksctlVerbs = set("create", "get", "delete", "update", "upgrade", "scale", "drain", "set", "unset",
		"enable", "disable", "utils", "associate", "anywhere", "register", "deregister", "completion",
		"version", "info", "help")
	eksctlValueFlags = set("--name", "-n", "--region", "-r", "--cluster", "--profile", "-p",
		"--config-file", "-f", "--nodegroup-name", "--timeout", "--color", "--verbose", "-v")

	pulumiVerbs = set("up", "update", "destroy", "down", "preview", "refresh", "stack", "state", "new",
		"login", "logout", "config", "import", "watch", "cancel", "console", "convert", "env", "gen-completion",
		"install", "logs", "org", "package", "plugin", "policy", "schema", "version", "whoami", "about")
	pulumiValueFlags = set("--stack", "-s", "--cwd", "-C", "--config-file", "--color", "--message", "-m",
		"--secrets-provider", "--target", "-t", "--parallel", "-p", "--policy-pack", "--diff-url", "--logflow")

	serverlessVerbs = set("deploy", "remove", "invoke", "logs", "info", "package", "print", "create",
		"install", "rollback", "metrics", "plugin", "offline", "login", "logout", "config", "doctor", "dev")
	serverlessValueFlags = set("--stage", "-s", "--region", "-r", "--config", "-c", "--aws-profile",
		"--function", "-f", "--param")

	cdktfVerbs = set("deploy", "destroy", "diff", "synth", "get", "init", "list", "login", "provider",
		"watch", "debug", "convert", "completion", "output", "version")
)

func matchCdk(args []string) (string, bool) {
	switch verb(args, cdkValueFlags, cdkVerbs) {
	case "destroy":
		return cdkDestroyDanger, true
	case "deploy", "watch":
		if value, ok := optionValue(args, "--require-approval"); ok && value == "never" {
			return cdkAutoApproveDanger, true
		}
	}
	return "", false
}

func matchPulumi(args []string) (string, bool) {
	pos := positionals(args, pulumiValueFlags)
	command := firstIn(pos, pulumiVerbs)

	switch command {
	case "destroy", "down":
		// --preview-only só mostra o que seria apagado.
		if !flagOn(args, "--preview-only", 0) {
			return pulumiDestroyDanger, true
		}
	case "up", "update":
		if flagOn(args, "--yes", 'y') {
			return pulumiYesDanger, true
		}
	case "stack":
		if sub := afterIn(pos, "stack"); (sub == "rm" || sub == "remove") && flagOn(args, "--force", 'f') {
			return pulumiForceDanger, true
		}
	case "state":
		if afterIn(pos, "state") == "delete" {
			return pulumiStateDanger, true
		}
	}
	return "", false
}

// verb devolve o primeiro argumento posicional que é um subcomando conhecido.
// Procurar entre os conhecidos (e não só olhar o primeiro) evita que uma opção
// com valor fora da lista esconda o subcomando.
func verb(args []string, valueFlags, known map[string]bool) string {
	return firstIn(positionals(args, valueFlags), known)
}

func firstIn(values []string, known map[string]bool) string {
	for _, v := range values {
		if known[v] {
			return v
		}
	}
	return ""
}

// afterIn devolve o argumento que vem logo depois de word.
func afterIn(values []string, word string) string {
	if i := slices.Index(values, word); i >= 0 && i+1 < len(values) {
		return values[i+1]
	}
	return ""
}

// hasFlag vale para "--flag", "--flag=valor".
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || (len(a) > len(flag) && a[:len(flag)] == flag && a[len(flag)] == '=') {
			return true
		}
	}
	return false
}

// optionValue lê "--opção valor" ou "--opção=valor".
func optionValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if len(a) > len(name) && a[:len(name)] == name && a[len(name)] == '=' {
			return a[len(name)+1:], true
		}
	}
	return "", false
}

// iacPackages: o nome do pacote npm que o npx/dlx recebe, no lugar do nome do programa.
var iacPackages = map[string]string{
	"aws-cdk":   "cdk",
	"cdktf-cli": "cdktf",
}

// iacProgram é o nome do programa sem caminho, sem "@versão" (npx aws-cdk@2) e
// com o nome do pacote trocado pelo do programa.
func iacProgram(token string) string {
	name := programName(token)
	if i := strings.LastIndex(name, "@"); i > 0 {
		name = name[:i]
	}
	if program, ok := iacPackages[name]; ok {
		return program
	}
	return name
}

var packageRunners = set("exec", "dlx", "x", "run", "run-script")

// packageRunnerCommand tira o "exec", "dlx", "run" do gerenciador de pacotes e
// devolve o comando que ele roda: npm exec -- cdk destroy → cdk destroy. Para
// pnpm e yarn, "pnpm cdk destroy" também roda o programa direto.
func packageRunnerCommand(args []string) []string {
	var rest []string
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		rest = args[i:]
		break
	}
	if len(rest) == 0 {
		return nil
	}
	if packageRunners[rest[0]] {
		rest = rest[1:]
	}

	// Sem as opções e os "--" do começo: o primeiro que sobra é o programa.
	var command []string
	for i, a := range rest {
		if strings.HasPrefix(a, "-") {
			continue
		}
		command = slices.DeleteFunc(slices.Clone(rest[i:]), func(x string) bool { return x == "--" })
		break
	}
	return command
}

// flagOn diz se uma opção booleana está ligada: "--flag", "--flag=true",
// "-y" e "-fy" (short é a letra; 0 = a opção não tem forma curta). "--flag=false"
// e "--flag=0" estão desligadas.
func flagOn(args []string, long string, short rune) bool {
	for _, a := range args {
		switch {
		case a == long:
			return true
		case strings.HasPrefix(a, long+"="):
			if v := a[len(long)+1:]; v != "false" && v != "0" {
				return true
			}
		case short != 0 && len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], short):
			return true
		}
	}
	return false
}
