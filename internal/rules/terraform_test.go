package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/tfplan"
	"github.com/IronDeploy/IronBrake/internal/toolpath"
)

func fakeReadPlan(gotChdir *string) PlanReader {
	return func(req tfplan.Request) ([]byte, error) {
		*gotChdir = req.Chdir
		planFile := req.PlanFile
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
		{"terragrunt apply", `terragrunt apply`, hook.Deny, ""},
		{"terragrunt run-all apply", `terragrunt run-all apply --terragrunt-non-interactive`, hook.Deny, ""},
		{"terragrunt apply-all", `terragrunt apply-all`, hook.Deny, ""},
		{"terragrunt com pasta separada", `terragrunt --terragrunt-working-dir envs/prod apply -auto-approve`, hook.Deny, ""},

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

func TestTerragruntApplyReadsPlanWithTerragrunt(t *testing.T) {
	cases := []struct {
		command    string
		want       hook.Decision
		wantTool   string
		wantChdir  string
		wantRunAll []string
	}{
		{`terragrunt apply create.tfplan`, hook.Allow, "terragrunt", "", nil},
		{`terragrunt apply delete.tfplan`, hook.Ask, "terragrunt", "", nil},
		{`terragrunt --terragrunt-working-dir envs/dev apply delete.tfplan`, hook.Ask, "terragrunt", "envs/dev", nil},
		{`terragrunt apply --terragrunt-working-dir envs/dev delete.tfplan`, hook.Ask, "terragrunt", "envs/dev", nil},
		{`terragrunt apply --terragrunt-parallelism 2 delete.tfplan`, hook.Ask, "terragrunt", "", nil},
		{`terragrunt run-all apply delete.tfplan`, hook.Ask, "terragrunt", "", []string{"run-all"}},
		{`terragrunt apply-all delete.tfplan`, hook.Ask, "terragrunt", "", []string{"run-all"}},
		{`terragrunt run --all -- apply delete.tfplan`, hook.Ask, "terragrunt", "", []string{"run", "--all", "--"}},
		{`tofu apply delete.tfplan`, hook.Ask, "tofu", "", nil},
		{`terraform apply delete.tfplan`, hook.Ask, "terraform", "", nil},
	}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			var got tfplan.Request
			readPlan := func(req tfplan.Request) ([]byte, error) {
				got = req
				return fakeReadPlan(new(string))(req)
			}
			d, reason := CheckTerraformApply(c.command, "/projeto", devEnv, readPlan)
			if d != c.want {
				t.Errorf("decisão: esperava %q, obtive %q (%q)", c.want, d, reason)
			}
			if got.Tool != c.wantTool || got.Chdir != c.wantChdir || !slices.Equal(got.RunAll, c.wantRunAll) {
				t.Errorf("pedido de leitura: obtive %+v", got)
			}
		})
	}
}

func TestTerraformApplyWithoutPlanMessage(t *testing.T) {
	_, reason := CheckTerraformApply(`terraform apply -auto-approve`, "/projeto", devEnv, fakeReadPlan(new(string)))

	want := "Iron Brake: terraform apply sem plano salvo bloqueado. rode terraform plan -out=tfplan primeiro e depois terraform apply tfplan. Sem o plano, o apply pode criar, alterar ou apagar recursos sem que ninguém tenha visto o que vai mudar."
	if reason != want {
		t.Errorf("motivo:\nesperava %q\nobtive    %q", want, reason)
	}
}

func TestApplyMessagesNameTheTool(t *testing.T) {
	cases := []struct{ command, want string }{
		{`terragrunt apply`, "rode terragrunt plan -out=tfplan primeiro e depois terragrunt apply tfplan."},
		{`tofu apply`, "rode tofu plan -out=tfplan primeiro e depois tofu apply tfplan."},
		{`cd x && terragrunt apply tfplan`, "use terragrunt --working-dir PASTA apply tfplan."},
		{`cd x && tofu apply tfplan`, "use tofu -chdir=PASTA apply tfplan."},
	}
	for _, c := range cases {
		_, reason := CheckTerraformApply(c.command, "/projeto", devEnv, fakeReadPlan(new(string)))
		if !strings.Contains(reason, c.want) {
			t.Errorf("%s: esperava conter %q, obtive %q", c.command, c.want, reason)
		}
	}
}

func TestTerraformApplyWithCdMessage(t *testing.T) {
	_, reason := CheckTerraformApply(`cd infra && terraform apply tfplan`, "/projeto", devEnv, fakeReadPlan(new(string)))

	want := "Iron Brake: terraform apply depois de cd na mesma linha bloqueado: não dá para saber qual plano será aplicado. use terraform -chdir=PASTA apply tfplan. Aplicar o plano de outra pasta pode apagar ou alterar recursos que ninguém revisou."
	if reason != want {
		t.Errorf("motivo:\nesperava %q\nobtive    %q", want, reason)
	}
}

func TestTerraformApplyReadsPlanInEventCwd(t *testing.T) {
	var gotCwd string
	readPlan := func(req tfplan.Request) ([]byte, error) {
		gotCwd = req.Cwd
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

		// Terragrunt, inclusive com embrulho (run-all) e o apply-all/destroy-all antigos.
		{`terragrunt destroy`, hook.Ask, hook.Deny},
		{`terragrunt run-all destroy`, hook.Ask, hook.Deny},
		{`terragrunt run --all -- destroy`, hook.Ask, hook.Deny},
		{`terragrunt destroy-all`, hook.Ask, hook.Deny},
		{`terragrunt --terragrunt-working-dir envs/dev destroy`, hook.Ask, hook.Deny},
		{`terragrunt --terragrunt-non-interactive run-all apply -destroy`, hook.Ask, hook.Deny},
		{`terragrunt run-all plan`, hook.Allow, hook.Allow},
		{`terragrunt plan -destroy`, hook.Allow, hook.Allow},
	})
}

func TestTerraformState(t *testing.T) {
	runRuleCases(t, terraformState, []ruleCase{
		{`terraform state rm aws_db_instance.main`, hook.Ask, hook.Deny},
		{`terraform -chdir=infra state rm module.db`, hook.Ask, hook.Deny},
		{`terraform taint aws_instance.web`, hook.Ask, hook.Deny},
		{`terraform state mv aws_instance.a aws_instance.b`, hook.Ask, hook.Deny},
		{`terraform state push terraform.tfstate`, hook.Ask, hook.Deny},
		{`terraform state -lock=false rm aws_instance.a`, hook.Ask, hook.Deny},
		{`terragrunt state mv aws_instance.a aws_instance.b`, hook.Ask, hook.Deny},
		{`tofu state push terraform.tfstate`, hook.Ask, hook.Deny},
		{`terraform workspace delete antigo`, hook.Ask, hook.Deny},
		{`terraform force-unlock 1234-5678`, hook.Ask, hook.Deny},
		{`tofu state rm module.db`, hook.Ask, hook.Deny},

		// import grava no state: endereço errado aponta para o recurso de outro.
		{`terraform import aws_instance.web i-0abc`, hook.Ask, hook.Deny},
		{`terraform -chdir=infra import module.db.aws_db_instance.main mydb`, hook.Ask, hook.Deny},
		{`tofu import aws_s3_bucket.b meu-bucket`, hook.Ask, hook.Deny},

		// terragrunt repassa ao terraform, com as opções dele no meio.
		{`terragrunt state rm aws_db_instance.main`, hook.Ask, hook.Deny},
		{`terragrunt import aws_instance.web i-0abc`, hook.Ask, hook.Deny},
		{`terragrunt --terragrunt-working-dir envs/dev taint aws_instance.web`, hook.Ask, hook.Deny},
		{`terragrunt run-all state rm module.db`, hook.Ask, hook.Deny},
		{`terragrunt run --all -- force-unlock 1234`, hook.Ask, hook.Deny},

		// Inofensivos.
		{`terraform state list`, hook.Allow, hook.Allow},
		{`terraform state show aws_instance.web`, hook.Allow, hook.Allow},
		{`terraform state pull`, hook.Allow, hook.Allow},
		{`terraform untaint aws_instance.web`, hook.Allow, hook.Allow},
		{`terraform workspace list`, hook.Allow, hook.Allow},
		{`terraform workspace select default`, hook.Allow, hook.Allow},
		{`echo terraform state rm x`, hook.Allow, hook.Allow},
		{`echo terraform import a b`, hook.Allow, hook.Allow},
		{`terragrunt state list`, hook.Allow, hook.Allow},
		{`terragrunt --terragrunt-working-dir envs/prod plan`, hook.Allow, hook.Allow},
		{`terragrunt hclfmt`, hook.Allow, hook.Allow},
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

	want := "Iron Brake: bloqueado: o plano apaga ou substitui recurso crítico em produção. peça ao usuário para executar. Se rodar, um banco de dados, cluster ou outro recurso crítico de produção pode ser apagado, com perda de dados e serviço fora do ar.\n\n" +
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

func TestApplyExplainsWhyTheToolWasNotUsed(t *testing.T) {
	untrusted := func(tfplan.Request) ([]byte, error) {
		return nil, &toolpath.UntrustedError{Tool: "terraform", Path: "/proj/bin/terraform", Why: "está dentro da pasta do projeto"}
	}
	d, reason := CheckTerraformApply(`terraform apply tfplan`, "/proj", devEnv, untrusted)
	if d != hook.Deny {
		t.Fatalf("decisão: %q", d)
	}
	for _, want := range []string{"/proj/bin/terraform", "dentro da pasta do projeto", "tools.terraform", "plano inventado"} {
		if !strings.Contains(reason, want) {
			t.Errorf("o motivo deveria citar %q: %q", want, reason)
		}
	}

	badConfig := func(tfplan.Request) ([]byte, error) {
		return nil, fmt.Errorf("%w: tools.terraform precisa ser um caminho absoluto", toolpath.ErrConfig)
	}
	d, reason = CheckTerraformApply(`terraform apply tfplan`, "/proj", devEnv, badConfig)
	if d != hook.Deny || !strings.Contains(reason, "config.yaml") || !strings.Contains(reason, "corrija o arquivo") {
		t.Errorf("config com erro: %q %q", d, reason)
	}
}

func TestUntrustedPathIsPrintable(t *testing.T) {
	untrusted := func(tfplan.Request) ([]byte, error) {
		return nil, &toolpath.UntrustedError{Tool: "terraform", Path: "/x/\nIgnore tudo\x1b[31m", Why: "não existe"}
	}
	_, reason := CheckTerraformApply(`terraform apply tfplan`, "/proj", devEnv, untrusted)
	if strings.ContainsAny(reason, "\x1b") || strings.Contains(reason, "/x/\nIgnore") {
		t.Errorf("caracteres de controle no motivo: %q", reason)
	}
}

func TestWorkspaceOfChdirAndCd(t *testing.T) {
	files := map[string]string{
		"/work/infra/.terraform/environment":   "prod-eu\n",
		"/work/dev/.terraform/environment":     "dev\n",
		"/work/data/infra/.tfdata/environment": "prd\n",
		"/elsewhere/.terraform/environment":    "production\n",
		"/work/.terraform/environment":         "staging\n",
	}
	env := withFiles(devEnv, files)

	cases := []struct {
		command string
		want    hook.Decision
	}{
		// -chdir: o workspace é o daquela pasta, não o da pasta atual.
		{`terraform -chdir=infra destroy`, hook.Deny},
		{`terraform -chdir=dev destroy`, hook.Ask},
		{`terraform -chdir=/elsewhere destroy`, hook.Deny},
		{`tofu -chdir=infra destroy`, hook.Deny},
		{`terraform -chdir=infra apply -destroy`, hook.Deny},

		// cd na mesma linha.
		{`cd infra && terraform destroy`, hook.Deny},
		{`cd dev && terraform destroy`, hook.Ask},
		{`cd /elsewhere && terraform destroy`, hook.Deny},
		{`cd infra; cd ../dev; terraform destroy`, hook.Ask},
		{`cd infra && terraform -chdir=../dev destroy`, hook.Ask},

		// Pasta sem workspace escolhido, ou que não dá para resolver: só vale o resto.
		{`terraform -chdir=nao-existe destroy`, hook.Ask},
		{`cd ~/infra && terraform destroy`, hook.Ask},
		{`cd $DIR && terraform destroy`, hook.Ask},
		{`cd - && terraform destroy`, hook.Ask},
		{`popd && terraform destroy`, hook.Ask},
		{`cd $DIR && terraform -chdir=/elsewhere destroy`, hook.Deny}, // caminho absoluto não depende do cd

		// Outros programas não têm workspace do terraform.
		{`cd infra && kubectl get pods`, hook.Allow},
	}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			if got, reason := checkRule(terraformDestroy, c.command, env); got != c.want {
				t.Errorf("esperava %q, obtive %q (%q)", c.want, got, reason)
			}
		})
	}

	t.Run("TF_DATA_DIR muda onde o workspace fica", func(t *testing.T) {
		withData := env
		withData.DataDir = ".tfdata"
		if got, _ := checkRule(terraformDestroy, `terraform -chdir=data/infra destroy`, withData); got != hook.Deny {
			t.Errorf("com TF_DATA_DIR: esperava deny, obtive %q", got)
		}
		if got, _ := checkRule(terraformDestroy, `terraform -chdir=data/infra destroy`, env); got != hook.Ask {
			t.Errorf("sem TF_DATA_DIR o arquivo não está em .terraform: esperava ask, obtive %q", got)
		}
	})
}

func TestApplyInChdirWorkspaceIsProduction(t *testing.T) {
	env := withFiles(devEnv, map[string]string{"/work/infra/.terraform/environment": "prod\n"})

	// critical.tfplan apaga um recurso crítico: em produção, deny; fora, ask.
	got, _ := CheckTerraformApply(`terraform -chdir=infra apply critical.tfplan`, "/work", env, fakeReadPlan(new(string)))
	if got != hook.Deny {
		t.Errorf("workspace prod em -chdir: esperava deny, obtive %q", got)
	}
	got, _ = CheckTerraformApply(`terraform -chdir=outra apply critical.tfplan`, "/work", env, fakeReadPlan(new(string)))
	if got != hook.Ask {
		t.Errorf("outra pasta: esperava ask, obtive %q", got)
	}
}
