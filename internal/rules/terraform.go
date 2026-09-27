package rules

import (
	"fmt"
	"slices"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/tfplan"
)

const (
	applyWithoutPlanReason = "Iron Brake: terraform apply sem plano salvo bloqueado. rode terraform plan -out=tfplan primeiro e depois terraform apply tfplan."
	unreadablePlanReason   = "Iron Brake: não consegui ler o plano salvo. rode terraform plan -out=tfplan em um comando separado e depois terraform apply tfplan."
	applyAfterCdReason     = "Iron Brake: terraform apply depois de cd na mesma linha bloqueado: não dá para saber qual plano será aplicado. use terraform -chdir=PASTA apply tfplan."
	criticalInProdReason   = "Iron Brake: bloqueado: o plano apaga ou substitui recurso crítico em produção. peça ao usuário para executar."
	destroyDanger          = "terraform destroy apaga todos os recursos do state."
)

var terraformDestroy = dangerRule{name: "terraform-destroy", match: matchTerraformDestroy}

// PlanReader devolve o JSON de um plano salvo (tfplan.Show).
type PlanReader func(cwd, chdir, planFile string) ([]byte, error)

// applyValueFlags aceitam o valor separado (-var 'a=b'), que não é o plano.
var applyValueFlags = map[string]bool{
	"backup": true, "exclude": true, "lock-timeout": true, "parallelism": true,
	"replace": true, "state": true, "state-out": true, "target": true,
	"var": true, "var-file": true,
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
	isApply, chdir, planFile := parseTerraformApply(tokens)
	if !isApply {
		return hook.Allow, "", nil
	}
	if planFile == "" {
		return hook.Deny, applyWithoutPlanReason, nil
	}
	// Depois de um cd, leríamos o plano da pasta errada.
	if changedDir {
		return hook.Deny, applyAfterCdReason, nil
	}

	data, err := readPlan(cwd, chdir, planFile)
	if err != nil {
		return hook.Deny, unreadablePlanReason, nil
	}
	summary, err := tfplan.Summarize(data)
	if err != nil {
		return hook.Deny, unreadablePlanReason, nil
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

// parseTerraformApply: terraform [globais] apply [opções] [PLANO].
func parseTerraformApply(tokens []string) (isApply bool, chdir, planFile string) {
	sub, chdir, args := terraformSubcommand(tokens)
	if sub != "apply" {
		return false, "", ""
	}

	for j := 0; j < len(args); j++ {
		arg := args[j]
		switch {
		case !strings.HasPrefix(arg, "-"):
			return true, chdir, arg
		case strings.Contains(arg, "="):
		case applyValueFlags[strings.TrimLeft(arg, "-")]:
			j++
		}
	}

	return true, chdir, ""
}

// terraformSubcommand: terraform [-chdir=DIR ...] SUBCOMANDO [argumentos].
func terraformSubcommand(tokens []string) (sub, chdir string, args []string) {
	if len(tokens) == 0 || programName(tokens[0]) != "terraform" {
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
