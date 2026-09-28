package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/tfplan"
)

func fakeReadPlan(gotChdir *string) PlanReader {
	return func(cwd, chdir, planFile string) ([]byte, error) {
		*gotChdir = chdir
		files := map[string]string{
			"create.tfplan":   "01-create-only.json",
			"delete.tfplan":   "03-with-delete.json",
			"replace.tfplan":  "04-with-replace.json",
			"critical.tfplan": "05-critical-handmade.json",
		}
		name, ok := files[planFile]
		if !ok {
			return nil, errors.New("plano não encontrado")
		}
		return os.ReadFile(filepath.Join("..", "..", "testdata", "plans", name))
	}
}

func TestCheckTerraformApply(t *testing.T) {
	cases := []struct {
		name      string
		command   string
		want      hook.Decision
		wantChdir string
	}{
		// Apply sem plano salvo: deny.
		{"apply puro", `terraform apply`, hook.Deny, ""},
		{"-auto-approve", `terraform apply -auto-approve`, hook.Deny, ""},
		{"-var com espaço não é plano", `terraform apply -var 'env=prod'`, hook.Deny, ""},
		{"-destroy", `terraform apply -destroy -auto-approve`, hook.Deny, ""},
		{"-chdir sem plano", `terraform -chdir=infra apply -auto-approve`, hook.Deny, ""},
		{"depois de &&", `cd infra && terraform apply -auto-approve`, hook.Deny, ""},
		{"caminho completo", `/opt/homebrew/bin/terraform apply`, hook.Deny, ""},

		// Plano salvo que não dá para ler: deny (na dúvida, trava).
		{"plano inexistente", `terraform apply missing.tfplan`, hook.Deny, ""},

		// cd na mesma linha: não dá para saber qual plano será aplicado. Deny.
		{"cd antes do apply", `cd infra && terraform apply create.tfplan`, hook.Deny, ""},
		{"pushd antes do apply", `pushd infra; terraform apply create.tfplan`, hook.Deny, ""},
		{"cd depois do apply não importa", `terraform apply create.tfplan && cd ..`, hook.Allow, ""},

		// Plano salvo só com criação: allow.
		{"plano só cria", `terraform apply create.tfplan`, hook.Allow, ""},
		{"plano com -auto-approve", `terraform apply -auto-approve create.tfplan`, hook.Allow, ""},
		{"plano com -chdir", `terraform -chdir=infra apply create.tfplan`, hook.Allow, "infra"},

		// Plano salvo que apaga ou substitui: ask com o cartão de risco.
		{"plano apaga", `terraform apply delete.tfplan`, hook.Ask, ""},
		{"plano substitui", `terraform apply -input=false replace.tfplan`, hook.Ask, ""},

		// Outros comandos: allow.
		{"terraform plan", `terraform plan -out=tfplan`, hook.Allow, ""},
		{"-chdir em outro subcomando", `terraform -chdir=infra plan`, hook.Allow, ""},
		{"apply como texto", `echo terraform apply`, hook.Allow, ""},
		{"vazio", ``, hook.Allow, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotChdir string
			got, reason := CheckTerraformApply(c.command, "/projeto", devEnv, fakeReadPlan(&gotChdir))

			if got != c.want {
				t.Errorf("decisão: esperava %q, obtive %q (motivo: %q)", c.want, got, reason)
			}
			if got != hook.Allow && reason == "" {
				t.Error("deny e ask precisam de motivo")
			}
			if gotChdir != c.wantChdir {
				t.Errorf("chdir: esperava %q, obtive %q", c.wantChdir, gotChdir)
			}
		})
	}
}

func TestTerraformApplyWithoutPlanMessage(t *testing.T) {
	_, reason := CheckTerraformApply(`terraform apply -auto-approve`, "/projeto", devEnv, fakeReadPlan(new(string)))

	want := "Iron Brake: terraform apply sem plano salvo bloqueado. rode terraform plan -out=tfplan primeiro e depois terraform apply tfplan."
	if reason != want {
		t.Errorf("motivo:\nesperava %q\nobtive    %q", want, reason)
	}
}

func TestTerraformApplyWithCdMessage(t *testing.T) {
	_, reason := CheckTerraformApply(`cd infra && terraform apply tfplan`, "/projeto", devEnv, fakeReadPlan(new(string)))

	want := "Iron Brake: terraform apply depois de cd na mesma linha bloqueado: não dá para saber qual plano será aplicado. use terraform -chdir=PASTA apply tfplan."
	if reason != want {
		t.Errorf("motivo:\nesperava %q\nobtive    %q", want, reason)
	}
}

func TestTerraformApplyReadsPlanInEventCwd(t *testing.T) {
	var gotCwd string
	readPlan := func(cwd, chdir, planFile string) ([]byte, error) {
		gotCwd = cwd
		return nil, errors.New("tanto faz")
	}

	CheckTerraformApply(`terraform apply tfplan`, "/projeto/infra", devEnv, readPlan)

	if gotCwd != "/projeto/infra" {
		t.Errorf("cwd: esperava %q, obtive %q", "/projeto/infra", gotCwd)
	}
}

func TestRiskCard(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{
			`terraform apply delete.tfplan`,
			"IRON BRAKE — terraform apply\n" +
				"Criar: 0 | Alterar: 0 | Apagar: 1 | Substituir: 0\n" +
				"Apagados ou substituídos:\n" +
				"- null_resource.marker[0] (apagar)",
		},
		{
			`terraform apply replace.tfplan`,
			"IRON BRAKE — terraform apply\n" +
				"Criar: 0 | Alterar: 0 | Apagar: 0 | Substituir: 2\n" +
				"Apagados ou substituídos:\n" +
				"- local_file.greeting (substituir)\n" +
				"- random_pet.name (substituir)",
		},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			_, reason := CheckTerraformApply(c.command, "/projeto", devEnv, fakeReadPlan(new(string)))

			if reason != c.want {
				t.Errorf("cartão:\nesperava:\n%s\n\nobtive:\n%s", c.want, reason)
			}
		})
	}
}

func TestTerraformDestroy(t *testing.T) {
	runRuleCases(t, terraformDestroy, []ruleCase{
		{`terraform destroy`, hook.Ask, hook.Deny},
		{`terraform destroy -auto-approve`, hook.Ask, hook.Deny},
		{`terraform -chdir=infra destroy`, hook.Ask, hook.Deny},
		{`/opt/homebrew/bin/terraform destroy -target=aws_instance.web`, hook.Ask, hook.Deny},
		{`terraform apply -destroy`, hook.Ask, hook.Deny},
		{`terraform apply -destroy -auto-approve`, hook.Ask, hook.Deny},
		{`terraform apply --destroy tfplan`, hook.Ask, hook.Deny},

		// Parecidos e inofensivos: allow em qualquer ambiente (nesta regra).
		{`terraform plan -destroy -out=tfplan`, hook.Allow, hook.Allow}, // só planeja
		{`terraform apply tfplan`, hook.Allow, hook.Allow},              // quem cuida é a regra do apply
		{`terraform state list`, hook.Allow, hook.Allow},
		{`terraform show`, hook.Allow, hook.Allow},
		{`terraform workspace list`, hook.Allow, hook.Allow},
		{`echo terraform destroy`, hook.Allow, hook.Allow},
		{`git commit -m "terraform destroy"`, hook.Allow, hook.Allow},

		// OpenTofu tem a mesma linha de comando.
		{`tofu destroy`, hook.Ask, hook.Deny},
		{`tofu apply -destroy`, hook.Ask, hook.Deny},
	})
}

func TestTerraformState(t *testing.T) {
	runRuleCases(t, terraformState, []ruleCase{
		{`terraform state rm aws_db_instance.main`, hook.Ask, hook.Deny},
		{`terraform -chdir=infra state rm module.db`, hook.Ask, hook.Deny},
		{`terraform taint aws_instance.web`, hook.Ask, hook.Deny},
		{`terraform workspace delete antigo`, hook.Ask, hook.Deny},
		{`terraform force-unlock 1234-5678`, hook.Ask, hook.Deny},
		{`tofu state rm module.db`, hook.Ask, hook.Deny},

		// Inofensivos.
		{`terraform state list`, hook.Allow, hook.Allow},
		{`terraform state show aws_instance.web`, hook.Allow, hook.Allow},
		{`terraform untaint aws_instance.web`, hook.Allow, hook.Allow},
		{`terraform workspace list`, hook.Allow, hook.Allow},
		{`terraform workspace select default`, hook.Allow, hook.Allow},
		{`echo terraform state rm x`, hook.Allow, hook.Allow},
	})
}

func TestTerraformApplyCriticalResourcesInProduction(t *testing.T) {
	cases := []struct {
		name    string
		command string
		env     Env
		want    hook.Decision
	}{
		{"crítico em produção", `terraform apply critical.tfplan`, prodEnv, hook.Deny},
		{"crítico, produção pelo -chdir", `terraform -chdir=envs/prod apply critical.tfplan`, devEnv, hook.Deny},
		{"crítico fora de produção", `terraform apply critical.tfplan`, devEnv, hook.Ask},
		{"não crítico em produção", `terraform apply delete.tfplan`, prodEnv, hook.Ask},
		{"só criação em produção", `terraform apply create.tfplan`, prodEnv, hook.Allow},
		// Política com erro: não sabemos o que é crítico, então tudo é.
		{"política com erro", `terraform apply delete.tfplan`, brokenEnv, hook.Deny},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := CheckTerraformApply(c.command, "/projeto", c.env, fakeReadPlan(new(string)))

			if got != c.want {
				t.Errorf("decisão: esperava %q, obtive %q (motivo: %q)", c.want, got, reason)
			}
		})
	}
}

func TestTerraformApplyCriticalMessage(t *testing.T) {
	_, reason := CheckTerraformApply(`terraform apply critical.tfplan`, "/projeto", prodEnv, fakeReadPlan(new(string)))

	want := "Iron Brake: bloqueado: o plano apaga ou substitui recurso crítico em produção. peça ao usuário para executar.\n\n" +
		"IRON BRAKE — terraform apply\n" +
		"Criar: 1 | Alterar: 0 | Apagar: 1 | Substituir: 1\n" +
		"Apagados ou substituídos:\n" +
		"- aws_db_instance.main (apagar)\n" +
		"- aws_s3_bucket.logs (substituir)"
	if reason != want {
		t.Errorf("motivo:\nesperava:\n%s\n\nobtive:\n%s", want, reason)
	}
}

func TestInspectTerraformApplyCountsResources(t *testing.T) {
	cases := []struct {
		command   string
		applies   int
		resources int
	}{
		{`terraform apply create.tfplan`, 1, 4},
		{`terraform apply delete.tfplan`, 1, 1},
		{`terraform apply replace.tfplan`, 1, 2},
		{`terraform apply critical.tfplan`, 1, 3},
		{`terraform apply create.tfplan && terraform apply delete.tfplan`, 2, 5},
		{`terraform apply -auto-approve`, 0, 0}, // sem plano: deny, não roda
		{`terraform apply missing.tfplan`, 0, 0},
		{`terraform plan -out=tfplan`, 0, 0},
		{`git status`, 0, 0},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			got := InspectTerraformApply(c.command, "/projeto", devEnv, fakeReadPlan(new(string)))

			if got.Applies != c.applies || got.Resources != c.resources {
				t.Errorf("esperava %d applies e %d recursos, obtive %d e %d", c.applies, c.resources, got.Applies, got.Resources)
			}
		})
	}
}

func TestRiskCardLimitsList(t *testing.T) {
	var s tfplan.Summary
	for i := range 30 {
		s.Delete++
		s.Destructive = append(s.Destructive, tfplan.Resource{Address: fmt.Sprintf("null_resource.n[%d]", i), Type: "null_resource", Action: "delete"})
	}

	card := riskCard(s)

	if lines := strings.Count(card, "\n- "); lines != 21 {
		t.Errorf("esperava 20 recursos + 1 linha de resumo, obtive %d linhas de lista:\n%s", lines, card)
	}
	if !strings.HasSuffix(card, "- ... e mais 10") {
		t.Errorf("esperava terminar com o resumo, obtive:\n%s", card)
	}
}
