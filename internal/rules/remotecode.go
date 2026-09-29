package rules

import (
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

const (
	pipeToShellDanger = "baixar um script da internet e executar direto no shell (curl | sh): você roda código que não viu, e ele herda suas credenciais."
	httpDeleteDanger  = "requisição HTTP DELETE: pode apagar recursos numa API do provedor sem passar pela CLI (foi assim que um agente apagou um banco de produção)."
)

// downloaders baixam conteúdo da rede.
var downloaders = set("curl", "wget")

// stdinInterpreters executam o que chega pela entrada padrão quando não
// recebem um script para rodar.
var stdinInterpreters = set("sh", "bash", "zsh", "dash", "ksh", "fish", "python", "python3", "node", "ruby", "perl", "php")

// remoteCodeRule olha a linha inteira: em curl URL | bash, o download e o
// shell estão em comandos diferentes (o tokenizer separa no pipe).
type remoteCodeRule struct{}

var remoteCode Rule = remoteCodeRule{}

func (remoteCodeRule) Name() string { return "remote-code" }

func (remoteCodeRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	for _, tokens := range commands {
		if hasHTTPDelete(tokens) {
			return decideByEnvironment(httpDeleteDanger, commands, env)
		}
		if hasCloudDeleteAction(tokens) {
			return decideByEnvironment(cloudAPIDanger, commands, env)
		}
	}
	if downloadsCode(commands) && runsFromStdin(commands) {
		return decideByEnvironment(pipeToShellDanger, commands, env)
	}
	return hook.Allow, ""
}

func downloadsCode(commands [][]string) bool {
	for _, tokens := range commands {
		if len(tokens) > 0 && downloaders[programName(tokens[0])] {
			return true
		}
	}
	return false
}

func runsFromStdin(commands [][]string) bool {
	for _, tokens := range commands {
		if isStdinInterpreter(tokens) {
			return true
		}
	}
	return false
}

// isStdinInterpreter: um interpretador sem arquivo de script roda o que vier
// pela entrada padrão. bash script.sh tem arquivo; bash e bash -s leem stdin.
func isStdinInterpreter(tokens []string) bool {
	if len(tokens) == 0 || !stdinInterpreters[programName(tokens[0])] {
		return false
	}
	for _, arg := range tokens[1:] {
		// Um positional (arquivo de script) significa que não lê o stdin.
		// -c/-e trazem o código no próprio argumento e já teriam sido
		// desembrulhados pelo tokenizer, então aqui não aparecem.
		if !strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return true
}

// hasHTTPDelete reconhece curl -X DELETE e wget --method=DELETE.
func hasHTTPDelete(tokens []string) bool {
	if len(tokens) == 0 || !downloaders[programName(tokens[0])] {
		return false
	}
	args := tokens[1:]
	for i, arg := range args {
		switch {
		case arg == "-X" || arg == "--request" || arg == "--method":
			if i+1 < len(args) && isDeleteMethod(args[i+1]) {
				return true
			}
		case strings.HasPrefix(arg, "-X"):
			if isDeleteMethod(strings.TrimPrefix(arg, "-X")) {
				return true
			}
		case strings.HasPrefix(arg, "--request="):
			if isDeleteMethod(strings.TrimPrefix(arg, "--request=")) {
				return true
			}
		case strings.HasPrefix(arg, "--method="):
			if isDeleteMethod(strings.TrimPrefix(arg, "--method=")) {
				return true
			}
		}
	}
	return false
}

func isDeleteMethod(value string) bool {
	return strings.EqualFold(value, "DELETE")
}
