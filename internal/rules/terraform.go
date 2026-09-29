package rules

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/tfplan"
	"github.com/IronDeploy/IronBrake/internal/toolpath"
)

const (
	applyWithoutPlanReason = "Iron Brake: terraform apply sem plano salvo bloqueado. rode terraform plan -out=tfplan primeiro e depois terraform apply tfplan. Sem o plano, o apply pode criar, alterar ou apagar recursos sem que ninguém tenha visto o que vai mudar."
	unreadablePlanReason   = "Iron Brake: não consegui ler o plano salvo. rode terraform plan -out=tfplan em um comando separado e depois terraform apply tfplan. Sem ler o plano, não dá para saber se o apply apaga ou substitui recursos."
	applyAfterCdReason     = "Iron Brake: terraform apply depois de cd na mesma linha bloqueado: não dá para saber qual plano será aplicado. use terraform -chdir=PASTA apply tfplan. Aplicar o plano de outra pasta pode apagar ou alterar recursos que ninguém revisou."
	criticalInProdReason   = "Iron Brake: bloqueado: o plano apaga ou substitui recurso crítico em produção. peça ao usuário para executar. Se rodar, um banco de dados, cluster ou outro recurso crítico de produção pode ser apagado, com perda de dados e serviço fora do ar."
	untrustedToolReason    = "Iron Brake: não usei o %s em %s para ler o plano: %s. instale-o num caminho confiável ou configure tools.%s (caminho absoluto) em ~/.iron/config.yaml, ou peça ao usuário para executar. Um programa falso no lugar poderia mostrar um plano inventado, e o apply real apagar ou alterar recursos sem aviso."
	toolConfigReason       = "Iron Brake: %s, então não sei qual programa usar para ler o plano. corrija o arquivo ou peça ao usuário para executar. Sem ler o plano, o apply pode apagar ou substituir recursos sem aviso."
	destroyDanger          = "terraform destroy apaga todos os recursos do state."

	stateRmDanger      = "terraform state rm tira o recurso do state sem destruí-lo de verdade: o próximo apply pode recriá-lo ou duplicá-lo."
	taintDanger        = "terraform taint força a destruição e a recriação do recurso no próximo apply."
	workspaceDelDanger = "terraform workspace delete apaga o workspace e o state associado a ele."
	forceUnlockDanger  = "terraform force-unlock remove o lock do state: com outro apply em andamento, o state pode corromper."
	importDanger       = "terraform import grava no state um recurso existente: com o endereço errado, o state passa a apontar para o recurso de outro e o próximo apply pode alterá-lo ou apagá-lo."
	stateMvDanger      = "terraform state mv muda o endereço de um recurso no state sem mexer na nuvem: com o destino errado, o próximo apply pode apagar o recurso real e criar outro no lugar."
	statePushDanger    = "terraform state push sobrescreve o state remoto pelo arquivo local: se ele estiver desatualizado, o próximo apply pode tentar recriar recursos que já existem ou perder o controle sobre eles."
)

var (
	terraformDestroy = dangerRule{name: "terraform-destroy", match: matchTerraformDestroy}
	terraformState   = dangerRule{name: "terraform-state", match: matchTerraformState}
)

// PlanReader devolve o JSON de um plano salvo (tfplan.Show).
type PlanReader func(tfplan.Request) ([]byte, error)

// applyValueFlags aceitam o valor separado (-var 'a=b'), que não é o plano.
var applyValueFlags = map[string]bool{
	"backup": true, "exclude": true, "lock-timeout": true, "parallelism": true,
	"replace": true, "state": true, "state-out": true, "target": true,
	"var": true, "var-file": true,

	// opções do terragrunt com valor separado (antigas com prefixo terragrunt-)
	"terragrunt-working-dir": true, "terragrunt-config": true, "terragrunt-source": true,
	"terragrunt-tfpath": true, "terragrunt-download-dir": true, "terragrunt-parallelism": true,
	"terragrunt-log-level": true, "terragrunt-iam-role": true,
	"working-dir": true, "config": true, "source": true, "tf-path": true,
	"download-dir": true, "log-level": true, "iam-role": true,
}

// CheckTerraformApply aplica "sem plano, sem apply": deny sem plano salvo ou
// com plano ilegível; ask com o cartão de risco se o plano apaga ou
// substitui; deny se isso atinge um recurso crítico em produção.
func CheckTerraformApply(command, cwd string, env Env, readPlan PlanReader) (hook.Decision, string) {
	result := InspectTerraformApply(command, cwd, env, readPlan)
	return result.Decision, result.Reason
}

// ApplyInspection soma à decisão os applies com plano lido e os recursos
// que eles alteram, para o limite por sessão e o log.
type ApplyInspection struct {
	Decision  hook.Decision
	Reason    string
	Applies   int
	Resources int
	Changes   []tfplan.Resource
}

const TerraformApplyRule = "terraform-apply"

func InspectTerraformApply(command, cwd string, env Env, readPlan PlanReader) ApplyInspection {
	result := ApplyInspection{Decision: hook.Allow}
	changedDir := false
	commands := splitCommands(command)
	production := env.isProduction(commands)

	for _, tokens := range commands {
		d, r, summary := checkApply(tokens, cwd, changedDir, production, env, readPlan)
		if summary != nil {
			result.Applies++
			result.Resources += summary.Changed()
			result.Changes = append(result.Changes, summary.Changes...)
		}
		if d == hook.Deny {
			result.Decision, result.Reason = d, r
			return result
		}
		if d == hook.Ask {
			result.Decision, result.Reason = d, r
		}
		if isChangeDir(tokens) {
			changedDir = true
		}
	}

	return result
}

func isChangeDir(tokens []string) bool {
	switch tokens[0] {
	case "cd", "pushd", "popd":
		return true
	}
	return false
}

// checkApply devolve também o resumo do plano, se conseguiu lê-lo.
func checkApply(tokens []string, cwd string, changedDir, production bool, env Env, readPlan PlanReader) (hook.Decision, string, *tfplan.Summary) {
	call, isApply := parseTerraformApply(tokens)
	if !isApply {
		return hook.Allow, "", nil
	}
	if call.PlanFile == "" {
		return hook.Deny, forTool(applyWithoutPlanReason, call.Tool), nil
	}
	// Depois de um cd, leríamos o plano da pasta errada.
	if changedDir {
		return hook.Deny, forTool(applyAfterCdReason, call.Tool), nil
	}

	call.Cwd = cwd
	data, err := readPlan(call)
	if err != nil {
		return hook.Deny, readPlanReason(err, call.Tool), nil
	}
	summary, err := tfplan.SummarizeAll(data)
	if err != nil {
		return hook.Deny, forTool(unreadablePlanReason, call.Tool), nil
	}

	if len(summary.Destructive) == 0 {
		return hook.Allow, "", &summary
	}
	if production && slices.ContainsFunc(summary.Destructive, func(r tfplan.Resource) bool {
		return env.isCritical(r.Type)
	}) {
		reason := criticalInProdReason
		if env.PolicyError {
			reason += policyErrorNote
		}
		return hook.Deny, reason + "\n\n" + riskCard(summary), &summary
	}
	return hook.Ask, riskCard(summary), &summary
}

// parseTerraformApply: terraform [globais] apply [opções] [PLANO]. Serve ao
// terragrunt também, que devolve como RunAll o embrulho (run-all) a repetir
// no show.
func parseTerraformApply(tokens []string) (call tfplan.Request, isApply bool) {
	sub, chdir, args := terraformSubcommand(tokens)
	if sub != "apply" {
		return tfplan.Request{}, false
	}
	call = tfplan.Request{Tool: programName(tokens[0]), Chdir: chdir}
	if call.Tool == "terragrunt" {
		call.RunAll = terragruntRunAll(tokens)
	}

	for j := 0; j < len(args); j++ {
		arg := args[j]
		switch {
		case !strings.HasPrefix(arg, "-"):
			call.PlanFile = arg
			return call, true
		case strings.Contains(arg, "="):
		case applyValueFlags[strings.TrimLeft(arg, "-")]:
			j++
		}
	}

	return call, true
}

// terragruntRunAll devolve como o comando embrulha o subcomando para rodar em
// todos os módulos, ou nil se roda em um só.
func terragruntRunAll(tokens []string) []string {
	for i, t := range tokens[1:] {
		switch t {
		case "run-all", "apply-all", "destroy-all":
			return []string{"run-all"}
		case "run":
			if slices.Contains(tokens[i+1:], "--all") {
				return []string{"run", "--all", "--"}
			}
		}
	}
	return nil
}

// terraformSubcommand: terraform [-chdir=DIR ...] SUBCOMANDO [argumentos].
// Aceita também o OpenTofu (tofu), que tem a mesma linha de comando, e o
// terragrunt, que repassa os subcomandos ao terraform.
func terraformSubcommand(tokens []string) (sub, chdir string, args []string) {
	if len(tokens) == 0 {
		return "", "", nil
	}
	switch programName(tokens[0]) {
	case "terraform", "tofu":
	case "terragrunt":
		return terragruntSubcommand(tokens)
	default:
		return "", "", nil
	}

	i := 1
	for ; i < len(tokens) && strings.HasPrefix(tokens[i], "-"); i++ {
		if dir, found := strings.CutPrefix(tokens[i], "-chdir="); found {
			chdir = dir
		}
	}
	if i == len(tokens) {
		return "", "", nil
	}
	return tokens[i], chdir, tokens[i+1:]
}

// terragruntCommands são os subcomandos que as regras conhecem. O terragrunt
// tem muitas opções com valor separado (--terragrunt-working-dir DIR) e
// embrulhos (run-all, run --all --), então procura o subcomando em vez de
// confiar na posição, como o kubectl.
var terragruntCommands = set(
	"apply", "destroy", "import", "state", "taint", "workspace", "force-unlock", "plan", "init",
	"show", "output", "validate", "fmt", "untaint", "refresh", "apply-all", "destroy-all",
)

// terragruntSubcommand devolve o subcomando do terraform que o terragrunt vai
// rodar. apply-all e destroy-all (versões antigas) valem como apply e destroy.
func terragruntSubcommand(tokens []string) (sub, chdir string, args []string) {
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		if flag, value, hasValue := strings.Cut(t, "="); flag == "--terragrunt-working-dir" || flag == "--working-dir" {
			switch {
			case hasValue:
				chdir = value
			case i+1 < len(tokens):
				chdir = tokens[i+1]
			}
		}
		if sub == "" && !strings.HasPrefix(t, "-") && terragruntCommands[t] {
			sub, args = strings.TrimSuffix(t, "-all"), tokens[i+1:]
		}
	}
	return sub, chdir, args
}

// matchTerraformDestroy: destroy e apply -destroy. O plan -destroy não conta;
// o apply desse plano passa pela regra do apply.
func matchTerraformDestroy(tokens []string) (string, bool) {
	sub, _, args := terraformSubcommand(tokens)
	if sub == "destroy" || (sub == "apply" && slices.ContainsFunc(args, isDestroyFlag)) {
		return destroyDanger, true
	}
	return "", false
}

func isDestroyFlag(arg string) bool {
	flag := strings.TrimLeft(arg, "-")
	return strings.HasPrefix(arg, "-") && (flag == "destroy" || flag == "destroy=true")
}

// matchTerraformState pega operações que mexem no state ou forçam recriação
// (import, state rm, taint...),
// fora do fluxo normal de plano/apply.
func matchTerraformState(tokens []string) (string, bool) {
	sub, _, args := terraformSubcommand(tokens)
	switch sub {
	case "state":
		switch firstPositional(args) {
		case "rm":
			return stateRmDanger, true
		case "mv":
			return stateMvDanger, true
		case "push":
			return statePushDanger, true
		}
	case "taint":
		return taintDanger, true
	case "workspace":
		if len(args) > 0 && args[0] == "delete" {
			return workspaceDelDanger, true
		}
	case "force-unlock":
		return forceUnlockDanger, true
	case "import":
		return importDanger, true
	}
	return "", false
}

// readPlanReason explica por que o plano não foi lido: programa não confiável,
// config.yaml com erro ou (o mais comum) plano ilegível.
func readPlanReason(err error, tool string) string {
	var untrusted *toolpath.UntrustedError
	switch {
	case errors.As(err, &untrusted):
		return fmt.Sprintf(untrustedToolReason, untrusted.Tool, printable(untrusted.Path), untrusted.Why, untrusted.Tool)
	case errors.Is(err, toolpath.ErrConfig):
		return fmt.Sprintf(toolConfigReason, err)
	}
	return forTool(unreadablePlanReason, tool)
}

// printable troca caracteres de controle: o caminho vai para o Claude e para a janela.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

// firstPositional devolve o primeiro argumento que não é opção (terraform
// state -lock=false rm ...), ou "".
func firstPositional(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// forTool troca "terraform" pelo nome da ferramenta do comando nas mensagens
// de apply, para o terragrunt e o tofu lerem "terragrunt plan -out=tfplan".
func forTool(reason, tool string) string {
	if tool == "" || tool == "terraform" {
		return reason
	}
	reason = strings.ReplaceAll(reason, "-chdir=PASTA", "--working-dir PASTA")
	if tool != "terragrunt" {
		reason = strings.ReplaceAll(reason, "--working-dir PASTA", "-chdir=PASTA")
	}
	return strings.ReplaceAll(reason, "terraform", tool)
}

// maxCardResources mantém a janela dentro da tela.
const maxCardResources = 20

func riskCard(s tfplan.Summary) string {
	var b strings.Builder
	b.WriteString("IRON BRAKE — terraform apply\n")
	fmt.Fprintf(&b, "Criar: %d | Alterar: %d | Apagar: %d | Substituir: %d",
		s.Create, s.Update, s.Delete, s.Replace)

	if len(s.Destructive) > 0 {
		b.WriteString("\nApagados ou substituídos:")
		for i, r := range s.Destructive {
			if i == maxCardResources {
				fmt.Fprintf(&b, "\n- ... e mais %d", len(s.Destructive)-maxCardResources)
				break
			}
			label := "apagar"
			if r.Action == "replace" {
				label = "substituir"
			}
			fmt.Fprintf(&b, "\n- %s (%s)", r.Address, label)
		}
	}

	return b.String()
}
