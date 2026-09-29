package rules

import (
	"path/filepath"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/policy"
)

const policyErrorNote = " (o .iron/policy.yaml tem erro, então o Iron Brake trata tudo como produção: corrija o arquivo.)"

// Env é o que as regras sabem sobre onde o comando vai rodar.
type Env struct {
	Policy      policy.Policy
	Context     []string // de runenv.Collect
	PolicyError bool     // policy.yaml com erro: tudo é produção e crítico

	Cwd      string                            // pasta do comando, para resolver caminhos relativos
	DataDir  string                            // TF_DATA_DIR ("" = .terraform), para achar o workspace do terraform
	ReadFile func(path string) ([]byte, error) // lê um arquivo local (safefile); nil = sem leitura
}

// readTargetFile lê um arquivo apontado por um comando (kubectl delete -f,
// psql -f). Devolve ok=false quando não dá para ler (sem leitor, remoto,
// stdin, diretório, arquivo inexistente): aí a decisão fica com o ambiente.
func (e Env) readTargetFile(path string) (data []byte, ok bool) {
	if e.ReadFile == nil || path == "" {
		return nil, false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.Cwd, path)
	}
	data, err := e.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// isProduction olha o contexto e todos os comandos da linha
// (cd envs/prod && terraform destroy também conta).
func (e Env) isProduction(commands [][]string) bool {
	if e.PolicyError {
		return true
	}
	if e.Policy.IsProduction(e.Context...) {
		return true
	}
	for _, tokens := range commands {
		if e.Policy.IsProduction(tokens...) {
			return true
		}
	}
	// terraform -chdir=infra e cd infra: o workspace é o daquela pasta.
	return e.Policy.IsProduction(e.workspaceTexts(commands)...)
}

func (e Env) isCritical(resourceType string) bool {
	return e.PolicyError || e.Policy.IsCritical(resourceType)
}

// Rule julga uma linha já quebrada em comandos (splitCommands).
type Rule interface {
	Name() string
	Check(commands [][]string, env Env) (hook.Decision, string)
}

// commandRule decide um comando por vez, sem olhar o ambiente.
type commandRule struct {
	name  string
	check func(tokens []string) (hook.Decision, string)
}

func (r commandRule) Name() string { return r.name }

func (r commandRule) Check(commands [][]string, _ Env) (hook.Decision, string) {
	decision, reason := hook.Allow, ""
	for _, tokens := range commands {
		d, why := r.check(tokens)
		if d == hook.Deny {
			return d, why
		}
		if d == hook.Ask && decision == hook.Allow {
			decision, reason = d, why
		}
	}
	return decision, reason
}

// dangerRule só reconhece o perigo; o ambiente decide: deny em produção, ask
// fora.
type dangerRule struct {
	name  string
	match func(tokens []string) (danger string, found bool)
}

func (r dangerRule) Name() string { return r.name }

func (r dangerRule) Check(commands [][]string, env Env) (hook.Decision, string) {
	for _, tokens := range commands {
		if danger, found := r.match(tokens); found {
			return decideByEnvironment(danger, commands, env)
		}
	}
	return hook.Allow, ""
}

func decideByEnvironment(danger string, commands [][]string, env Env) (hook.Decision, string) {
	if !env.isProduction(commands) {
		return hook.Ask, "Iron Brake: confirme antes de executar: " + danger
	}
	reason := "Iron Brake: bloqueado em produção: " + danger + " peça ao usuário para executar."
	if env.PolicyError {
		reason += policyErrorNote
	}
	return hook.Deny, reason
}

// decideUnreadableTarget vale quando o alvo destrutivo está num arquivo que o
// Iron Brake não conseguiu ver (remoto, stdin, kustomize, inexistente): em
// produção pede confirmação; fora dela, libera (o conteúdo pode ser inofensivo).
func decideUnreadableTarget(what string, commands [][]string, env Env) (hook.Decision, string) {
	if !env.isProduction(commands) {
		return hook.Allow, ""
	}
	reason := "Iron Brake: não consegui ver o conteúdo de " + what + " em produção; confirme antes de executar."
	if env.PolicyError {
		reason += policyErrorNote
	}
	return hook.Ask, reason
}

// Verdict é a decisão, o motivo e a regra que decidiu (vazia no allow).
type Verdict struct {
	Decision hook.Decision
	Reason   string
	Rule     string
}

// CheckAll roda o registro: deny vence; senão vale o primeiro ask.
func CheckAll(command string, env Env) (hook.Decision, string) {
	v := Evaluate(command, env)
	return v.Decision, v.Reason
}

// Evaluate é CheckAll com o nome da regra que decidiu.
func Evaluate(command string, env Env) Verdict {
	return runRules(splitCommands(command), env, registry)
}

func runRules(commands [][]string, env Env, rules []Rule) Verdict {
	result := Verdict{Decision: hook.Allow}
	for _, r := range rules {
		d, why := r.Check(commands, env)
		if d == hook.Deny {
			return Verdict{Decision: d, Reason: why, Rule: r.Name()}
		}
		if d == hook.Ask && result.Decision == hook.Allow {
			result = Verdict{Decision: d, Reason: why, Rule: r.Name()}
		}
	}
	return result
}

// positionals devolve os argumentos que não são opções nem valores das
// opções em valueFlags (-n prod).
func positionals(args []string, valueFlags map[string]bool) []string {
	var result []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case !strings.HasPrefix(arg, "-") || arg == "-":
			result = append(result, arg)
		case valueFlags[arg]:
			i++
		}
	}
	return result
}
