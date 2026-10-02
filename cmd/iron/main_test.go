package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/dialog"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/policy"
	"github.com/IronDeploy/IronBrake/internal/rules"
	"github.com/IronDeploy/IronBrake/internal/session"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/testutil/fakesh"
	"github.com/IronDeploy/IronBrake/internal/tfplan"
)

func openEvent(t *testing.T, file string) *os.File {
	t.Helper()

	input, err := os.Open(filepath.Join("..", "..", "testdata", "events", file))
	if err != nil {
		t.Fatalf("erro abrindo evento: %v", err)
	}
	t.Cleanup(func() { input.Close() })

	return input
}

func TestMain(m *testing.M) {
	fakesh.MaybeRun()
	os.Exit(m.Run())
}

// setHome aponta o home do usuário para dir. No Windows o Go lê USERPROFILE,
// não HOME: sem os dois o teste gravaria no home real.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func fakeReadPlan(req tfplan.Request) ([]byte, error) {
	planFile := req.PlanFile
	files := map[string]string{
		"create.tfplan":  "01-create-only.json",
		"delete.tfplan":  "03-with-delete.json",
		"replace.tfplan": "04-with-replace.json",
	}
	name, ok := files[planFile]
	if !ok {
		return nil, errors.New("plano não encontrado")
	}
	return os.ReadFile(filepath.Join("..", "..", "testdata", "plans", name))
}

type fakeDialog struct {
	answer   dialog.Answer
	messages []string
}

func (f *fakeDialog) confirm(message string) dialog.Answer {
	f.messages = append(f.messages, message)
	return f.answer
}

func noDialog(string) dialog.Answer { return dialog.Unavailable }

func devEnv(cwd string) rules.Env {
	return rules.Env{Policy: policy.Default(), Context: []string{cwd}}
}

func noSession(string, session.Activity) (hook.Decision, string) { return hook.Allow, "" }

func discardAudit(audit.Entry, policy.AuditLog) error { return nil }

type fakeAudit struct {
	entries []audit.Entry
	err     error
}

func (f *fakeAudit) append(e audit.Entry, _ policy.AuditLog) error {
	f.entries = append(f.entries, e)
	return f.err
}

func testDeps() hookDeps {
	return hookDeps{readPlan: fakeReadPlan, confirm: noDialog, loadEnv: devEnv, session: noSession, audit: discardAudit, deadline: time.Minute}
}

func TestRunHook(t *testing.T) {
	cases := []struct {
		file     string
		wantCode int
	}{
		{"git-status.json", 0},
		{"read-tool.json", 0},
		{"git-push-force.json", 2},
		// terraform apply sem plano salvo: deny.
		{"terraform-apply.json", 2},
		// terraform apply com plano que só cria: allow.
		{"terraform-apply-plan-create.json", 0},
		// kubectl delete namespace: deny.
		{"kubectl-delete-namespace.json", 2},
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := runHook(openEvent(t, c.file), &stdout, &stderr, testDeps())

			if code != c.wantCode {
				t.Errorf("código de saída: esperava %d, obtive %d", c.wantCode, code)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout deveria estar vazio, obtive %q", stdout.String())
			}
			// deny escreve o motivo no stderr; allow é silencioso.
			if (c.wantCode == 2) != (stderr.Len() > 0) {
				t.Errorf("stderr inesperado para o código %d: %q", c.wantCode, stderr.String())
			}
		})
	}
}

func TestRunHookAskWritesJSONAndExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runHook(openEvent(t, "git-push-force-with-lease.json"), &stdout, &stderr, testDeps())

	if code != 0 {
		t.Errorf("código de saída: esperava 0, obtive %d", code)
	}
	if !strings.Contains(stdout.String(), `"permissionDecision":"ask"`) {
		t.Errorf("stdout deveria trazer o JSON do ask, obtive %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr deveria estar vazio, obtive %q", stderr.String())
	}
}

func TestRunHookTerraformDestructivePlanWithoutDialogFallsBackToAsk(t *testing.T) {
	cases := []struct {
		file string
		card string
	}{
		{
			"terraform-apply-plan-delete.json",
			"IRON BRAKE — terraform apply\n" +
				"Criar: 0 | Alterar: 0 | Apagar: 1 | Substituir: 0\n" +
				"Apagados ou substituídos:\n" +
				"- null_resource.marker[0] (apagar)",
		},
		{
			"terraform-apply-plan-replace.json",
			"IRON BRAKE — terraform apply\n" +
				"Criar: 0 | Alterar: 0 | Apagar: 0 | Substituir: 2\n" +
				"Apagados ou substituídos:\n" +
				"- local_file.greeting (substituir)\n" +
				"- random_pet.name (substituir)",
		},
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := runHook(openEvent(t, c.file), &stdout, &stderr, testDeps())

			if code != 0 {
				t.Errorf("código de saída: esperava 0, obtive %d", code)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr deveria estar vazio, obtive %q", stderr.String())
			}

			var out struct {
				HookSpecificOutput struct {
					PermissionDecision       string `json:"permissionDecision"`
					PermissionDecisionReason string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
				SystemMessage string `json:"systemMessage"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatalf("stdout não é JSON: %v (%q)", err, stdout.String())
			}
			if out.HookSpecificOutput.PermissionDecision != "ask" {
				t.Errorf("permissionDecision: esperava ask, obtive %q", out.HookSpecificOutput.PermissionDecision)
			}
			if out.SystemMessage != c.card {
				t.Errorf("systemMessage:\nesperava:\n%s\n\nobtive:\n%s", c.card, out.SystemMessage)
			}
			if out.HookSpecificOutput.PermissionDecisionReason != c.card {
				t.Errorf("permissionDecisionReason:\nesperava:\n%s\n\nobtive:\n%s", c.card, out.HookSpecificOutput.PermissionDecisionReason)
			}
		})
	}
}

const deleteCard = "IRON BRAKE — terraform apply\n" +
	"Criar: 0 | Alterar: 0 | Apagar: 1 | Substituir: 0\n" +
	"Apagados ou substituídos:\n" +
	"- null_resource.marker[0] (apagar)"

func TestRunHookDestructivePlanApprovedInDialog(t *testing.T) {
	d := &fakeDialog{answer: dialog.Approved}
	var stdout, stderr bytes.Buffer

	code := runHook(openEvent(t, "terraform-apply-plan-delete.json"), &stdout, &stderr, hookDeps{readPlan: fakeReadPlan, confirm: d.confirm, loadEnv: devEnv, session: noSession, audit: discardAudit, deadline: time.Minute})

	if code != 0 {
		t.Errorf("código de saída: esperava 0, obtive %d", code)
	}
	if !strings.Contains(stdout.String(), `"permissionDecision":"allow"`) {
		t.Errorf("stdout deveria trazer o allow explícito, obtive %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr deveria estar vazio, obtive %q", stderr.String())
	}
	if len(d.messages) != 1 || d.messages[0] != deleteCard {
		t.Errorf("a janela deveria mostrar o cartão uma vez; mostrou %q", d.messages)
	}
}

func TestRunHookApprovedPlanWithChainedCommandHasNoOpinion(t *testing.T) {
	d := &fakeDialog{answer: dialog.Approved}
	event := `{"tool_name":"Bash","cwd":"/projeto","tool_input":{"command":"terraform apply delete.tfplan && rm -rf ~/projeto"}}`
	var stdout, stderr bytes.Buffer

	code := runHook(strings.NewReader(event), &stdout, &stderr, hookDeps{readPlan: fakeReadPlan, confirm: d.confirm, loadEnv: devEnv, session: noSession, audit: discardAudit, deadline: time.Minute})

	if code != 0 {
		t.Errorf("código de saída: esperava 0, obtive %d", code)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("sem opinião é silencioso; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunHookDestructivePlanRejectedInDialog(t *testing.T) {
	d := &fakeDialog{answer: dialog.Rejected}
	var stdout, stderr bytes.Buffer

	code := runHook(openEvent(t, "terraform-apply-plan-delete.json"), &stdout, &stderr, hookDeps{readPlan: fakeReadPlan, confirm: d.confirm, loadEnv: devEnv, session: noSession, audit: discardAudit, deadline: time.Minute})

	if code != 2 {
		t.Errorf("código de saída: esperava 2, obtive %d", code)
	}
	want := "Iron Brake: o usuário recusou este comando na janela de confirmação. não tente de novo; siga com o resto da tarefa ou pergunte ao usuário o que fazer.\n\n" + deleteCard + "\n"
	if stderr.String() != want {
		t.Errorf("stderr:\nesperava %q\nobtive    %q", want, stderr.String())
	}
}

func TestRunHookDialogForEveryAsk(t *testing.T) {
	cases := []struct {
		file       string
		wantDialog bool
	}{
		{"terraform-apply.json", false},             // deny: sem plano
		{"terraform-apply-plan-create.json", false}, // allow: só cria
		{"git-status.json", false},                  // allow
		{"git-push-force-with-lease.json", true},    // ask da regra de force push
		{"terraform-apply-plan-delete.json", true},  // ask do cartão de risco
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			d := &fakeDialog{answer: dialog.Rejected}
			var stdout, stderr bytes.Buffer

			runHook(openEvent(t, c.file), &stdout, &stderr, hookDeps{readPlan: fakeReadPlan, confirm: d.confirm, loadEnv: devEnv, session: noSession, audit: discardAudit, deadline: time.Minute})

			if opened := len(d.messages) == 1; opened != c.wantDialog {
				t.Errorf("janela: esperava aberta=%v; mostrou %q", c.wantDialog, d.messages)
			}
		})
	}
}

func loopDeps(t *testing.T, now *time.Time) hookDeps {
	t.Helper()
	store := session.Store{Dir: t.TempDir()}
	deps := testDeps()
	deps.session = func(id string, a session.Activity) (hook.Decision, string) {
		return store.Check(id, a, *now)
	}
	return deps
}

func runEvent(t *testing.T, deps hookDeps, command string) (int, string) {
	t.Helper()
	event := `{"session_id":"sessao-loop","tool_name":"Bash","cwd":"/srv/loja","tool_input":{"command":` + strconv.Quote(command) + `}}`
	var stdout, stderr bytes.Buffer
	code := runHook(strings.NewReader(event), &stdout, &stderr, deps)
	return code, stdout.String()
}

func TestRunHookRepeatedInfraCommand(t *testing.T) {
	now := time.Date(2026, 1, 10, 14, 0, 0, 0, time.UTC)
	deps := loopDeps(t, &now)

	// kubectl get pods 6 vezes em 3 minutos: allow, allow, ask, ask, ask, deny.
	wantCodes := []int{0, 0, 0, 0, 0, 2}
	wantAsk := []bool{false, false, true, true, true, false}
	for i := range wantCodes {
		code, out := runEvent(t, deps, "kubectl get pods")
		if code != wantCodes[i] || strings.Contains(out, `"ask"`) != wantAsk[i] {
			t.Errorf("vez %d: código %d, stdout %q", i+1, code, out)
		}
		now = now.Add(30 * time.Second)
	}
}

func TestRunHookRepeatedNonInfraCommand(t *testing.T) {
	now := time.Date(2026, 1, 10, 14, 0, 0, 0, time.UTC)
	deps := loopDeps(t, &now)

	for i := range 10 {
		if code, out := runEvent(t, deps, "go test ./..."); code != 0 || out != "" {
			t.Fatalf("vez %d: esperava allow, obtive código %d, stdout %q", i+1, code, out)
		}
		now = now.Add(10 * time.Second)
	}
}

func TestRunHookFourthApplyAsks(t *testing.T) {
	now := time.Date(2026, 1, 10, 14, 0, 0, 0, time.UTC)
	deps := loopDeps(t, &now)

	for i := range 4 {
		code, out := runEvent(t, deps, "terraform apply create.tfplan")
		wantAsk := i == 3
		if code != 0 || strings.Contains(out, `"ask"`) != wantAsk {
			t.Errorf("apply %d: código %d, stdout %q", i+1, code, out)
		}
		now = now.Add(10 * time.Minute) // longe o bastante para não contar repetição
	}
}

func TestRunHookReadsPlanInEventCwd(t *testing.T) {
	var gotCwd string
	readPlan := func(req tfplan.Request) ([]byte, error) {
		gotCwd = req.Cwd
		return fakeReadPlan(req)
	}
	var stdout, stderr bytes.Buffer

	runHook(openEvent(t, "terraform-apply-plan-create.json"), &stdout, &stderr, hookDeps{readPlan: readPlan, confirm: noDialog, loadEnv: devEnv, session: noSession, audit: discardAudit, deadline: time.Minute})

	if want := "/Users/user/Documents/IronDeploy/infra"; gotCwd != want {
		t.Errorf("cwd: esperava %q, obtive %q", want, gotCwd)
	}
}

func isolateEnv(t *testing.T, policyYAML string) {
	t.Helper()

	project := t.TempDir()
	if policyYAML != "" {
		path := policy.Path(project)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(policyYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CLAUDE_PROJECT_DIR", project)
	setHome(t, t.TempDir())
	for _, name := range []string{"KUBECONFIG", "TF_WORKSPACE", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "CLOUDSDK_ACTIVE_CONFIG_NAME", "CLOUDSDK_CORE_PROJECT"} {
		t.Setenv(name, "")
	}
}

func TestRunHookDecisionDependsOnPolicyFile(t *testing.T) {
	event := `{"tool_name":"Bash","cwd":"/srv/loja","tool_input":{"command":"kubectl delete namespace app"}}`
	cases := []struct {
		name     string
		policy   string
		wantCode int
		wantOut  string
	}{
		// Sem arquivo: "loja" não é produção → ask (JSON no stdout, código 0).
		{"sem policy.yaml", "", 0, `"permissionDecision":"ask"`},
		// O arquivo diz que "loja" é produção → deny.
		{"loja é produção", "production_patterns:\n  - loja\n", 2, ""},
		// Arquivo com erro → tratado como produção → deny, nunca allow.
		{"policy.yaml com erro", "production_patterns: [loja\n", 2, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateEnv(t, c.policy)
			var stdout, stderr bytes.Buffer

			code := runHook(strings.NewReader(event), &stdout, &stderr, hookDeps{readPlan: fakeReadPlan, confirm: noDialog, loadEnv: loadEnv, session: noSession, audit: discardAudit, deadline: time.Minute})

			if code != c.wantCode {
				t.Errorf("código de saída: esperava %d, obtive %d (stderr=%q)", c.wantCode, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), c.wantOut) {
				t.Errorf("stdout: esperava conter %q, obtive %q", c.wantOut, stdout.String())
			}
		})
	}
}

func TestRunHookBrokenPolicy(t *testing.T) {
	isolateEnv(t, "production_pattern: [typo]\n")
	deps := hookDeps{readPlan: fakeReadPlan, confirm: noDialog, loadEnv: loadEnv, session: noSession, audit: discardAudit, deadline: time.Minute}

	var stdout, stderr bytes.Buffer
	code := runHook(strings.NewReader(`{"tool_name":"Bash","cwd":"/srv/loja","tool_input":{"command":"terraform destroy"}}`), &stdout, &stderr, deps)
	if code != 2 || !strings.Contains(stderr.String(), "policy.yaml") {
		t.Errorf("esperava deny citando o policy.yaml; código %d, stderr %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runHook(strings.NewReader(`{"tool_name":"Bash","cwd":"/srv/loja","tool_input":{"command":"git status"}}`), &stdout, &stderr, deps)
	if code != 0 || stdout.Len() != 0 {
		t.Errorf("git status deveria continuar liberado; código %d, stdout %q", code, stdout.String())
	}
}

func TestRunHookInvalidInputBlocksWithoutEchoingContent(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runHook(strings.NewReader(`{"tool_input": "segredo-123`), &stdout, &stderr, testDeps())

	if code != 2 {
		t.Errorf("código de saída: esperava 2, obtive %d", code)
	}
	if stderr.Len() == 0 {
		t.Error("stderr deveria avisar que o evento é inválido")
	}
	if strings.Contains(stderr.String(), "segredo-123") {
		t.Errorf("stderr vazou o conteúdo da entrada: %q", stderr.String())
	}
}

const testExe = "/opt/my tools/iron"

func TestRunInit(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, ".claude", "settings.json")

	t.Run("primeira vez instala", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		code := runInit(dir, testExe, initOptions{}, &stdout, &stderr)

		if code != 0 {
			t.Fatalf("código de saída: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "instalado") {
			t.Errorf("stdout deveria dizer que instalou: %q", stdout.String())
		}
		data, err := os.ReadFile(settings)
		if err != nil {
			t.Fatalf("o settings.json deveria existir: %v", err)
		}
		if !strings.Contains(string(data), testExe) {
			t.Errorf("o settings.json deveria apontar para %q:\n%s", testExe, data)
		}
	})

	t.Run("segunda vez avisa que já estava", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		code := runInit(dir, testExe, initOptions{}, &stdout, &stderr)

		if code != 0 {
			t.Fatalf("código de saída: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "já estava") {
			t.Errorf("stdout deveria dizer que já estava instalado: %q", stdout.String())
		}
	})
}

func TestRunInitHardenWritesDenyRules(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, ".claude", "settings.json")
	var stdout, stderr bytes.Buffer

	code := runInit(dir, testExe, initOptions{harden: true}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("código de saída: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "credenciais") {
		t.Errorf("stdout deveria mencionar as regras de credenciais: %q", stdout.String())
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"Read(~/.aws/**)", "Read(~/.ssh/**)", "Read(**/.env)"} {
		if !strings.Contains(string(data), rule) {
			t.Errorf("settings.json deveria conter a regra %q:\n%s", rule, data)
		}
	}
}

func TestRunInitInvalidSettingsFailsWithoutTouchingFile(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, ".claude", "settings.json")
	original := `{"env": {"TOKEN": "s3cr3t-value"},}`
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := runInit(dir, testExe, initOptions{}, &stdout, &stderr)

	if code == 0 {
		t.Error("esperava um código de saída diferente de 0")
	}
	if stderr.Len() == 0 {
		t.Error("stderr deveria explicar o erro")
	}
	if strings.Contains(stderr.String(), "s3cr3t-value") {
		t.Errorf("stderr vazou o conteúdo do arquivo: %q", stderr.String())
	}
	if got, _ := os.ReadFile(settings); string(got) != original {
		t.Errorf("o arquivo foi alterado: %s", got)
	}
}

func TestRunShieldStatusLockUnlock(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	setHome(t, t.TempDir()) // isola o log de auditoria de ~/.iron real
	settings := filepath.Join(dir, ".claude", "settings.json")

	t.Run("status sem iron init nem harden: tudo destravado", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"shield", "status"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("código: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "DESTRAVADO") {
			t.Errorf("esperava reportar DESTRAVADO: %q", stdout.String())
		}
		if _, err := os.Stat(settings); !os.IsNotExist(err) {
			t.Error("status não deveria criar o settings.json")
		}
	})

	t.Run("lock funciona sem iron init ter rodado antes", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"shield", "lock"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("código: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "travado") {
			t.Errorf("esperava confirmar o travamento: %q", stdout.String())
		}
		data, err := os.ReadFile(settings)
		if err != nil {
			t.Fatalf("lock deveria ter criado o settings.json: %v", err)
		}
		if !strings.Contains(string(data), "Read(~/.aws/**)") {
			t.Errorf("settings.json deveria conter as regras do Iron Shield:\n%s", data)
		}
	})

	t.Run("status depois do lock: tudo travado", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"shield", "status"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("código: esperava 0, obtive %d", code)
		}
		if !strings.Contains(stdout.String(), "TRAVADO") || strings.Contains(stdout.String(), "DESTRAVADO") {
			t.Errorf("esperava reportar TRAVADO (e não DESTRAVADO): %q", stdout.String())
		}
	})

	t.Run("unlock destrava e preserva o resto do settings", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"shield", "unlock"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("código: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "DESTRAVADO") {
			t.Errorf("esperava confirmar o destravamento: %q", stdout.String())
		}
		data, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "Read(~/.aws/**)") {
			t.Errorf("unlock deveria ter removido as regras do Iron Shield:\n%s", data)
		}
		// O hook instalado por um "iron init" anterior (se houvesse) não seria
		// tocado; aqui confirmamos que o arquivo continua um JSON válido com a
		// chave permissions ainda presente (mesmo que a lista fique vazia).
		if !strings.Contains(string(data), "permissions") {
			t.Errorf("settings.json não deveria perder a chave permissions:\n%s", data)
		}
	})

	t.Run("unlock de novo: nada a fazer, sem erro", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"shield", "unlock"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("código: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "já estava destravado") {
			t.Errorf("esperava avisar que já estava destravado: %q", stdout.String())
		}
	})
}

func TestRunShieldUnlockWithoutAnySettingsIsANoop(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	setHome(t, t.TempDir())
	var stdout, stderr bytes.Buffer

	code := run([]string{"shield", "unlock"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("código: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "já estava destravado") {
		t.Errorf("esperava avisar que já estava destravado: %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("unlock sem lock/harden anterior não deveria criar o settings.json")
	}
}

func TestRunShieldBadSubcommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer

	code := run([]string{"shield", "banana"}, strings.NewReader(""), &stdout, &stderr)

	if code != 2 {
		t.Errorf("código: esperava 2, obtive %d", code)
	}
	if !strings.Contains(stderr.String(), "banana") {
		t.Errorf("stderr deveria citar o subcomando desconhecido: %q", stderr.String())
	}
}

func TestRunShieldLockUnlockWriteAuditEntries(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	home := t.TempDir()
	setHome(t, home)

	run([]string{"shield", "lock"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	run([]string{"shield", "unlock"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})

	data, err := os.ReadFile(filepath.Join(home, ".iron", "audit.log"))
	if err != nil {
		t.Fatalf("esperava um log de auditoria em ~/.iron/audit.log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, `"class":"iron shield"`) || !strings.Contains(log, `"decision":"lock"`) {
		t.Errorf("esperava registrar o lock no log de auditoria:\n%s", log)
	}
	if !strings.Contains(log, `"decision":"unlock"`) {
		t.Errorf("esperava registrar o unlock no log de auditoria:\n%s", log)
	}
}

func TestRunDoctor(t *testing.T) {
	// Cada caso isola o HOME: o doctor confere o log de auditoria de ~/.iron.
	t.Run("tudo certo sai com 0", func(t *testing.T) {
		setHome(t, t.TempDir())
		t.Setenv("PATH", t.TempDir()) // nenhum terraform: o resultado não depende da máquina
		dir := t.TempDir()
		// Um "iron" de mentira que bloqueia, instalado pelo próprio init:
		// assim o teste cobre o ciclo init -> doctor.
		iron := fakesh.Install(t, filepath.Join(dir, "bin", "iron"), "#!/bin/sh\necho bloqueado >&2\nexit 2\n")
		if _, err := setup.InstallHook(setup.SettingsPath(dir), iron); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer

		code := runDoctor(dir, &stdout)

		if code != 0 {
			t.Errorf("código de saída: esperava 0, obtive %d\n%s", code, stdout.String())
		}
		if !strings.Contains(stdout.String(), "Tudo certo") {
			t.Errorf("a saída deveria dizer que está tudo certo:\n%s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "log de auditoria") {
			t.Errorf("a saída deveria incluir a verificação do log de auditoria:\n%s", stdout.String())
		}
	})

	t.Run("log de auditoria sem gravação faz o doctor falhar", func(t *testing.T) {
		setHome(t, t.TempDir())
		t.Setenv("PATH", t.TempDir())
		dir := t.TempDir()
		iron := fakesh.Install(t, filepath.Join(dir, "bin", "iron"), "#!/bin/sh\nexit 2\n")
		if _, err := setup.InstallHook(setup.SettingsPath(dir), iron); err != nil {
			t.Fatal(err)
		}
		// Um diretório no lugar do arquivo do log: nada grava ali.
		if err := os.MkdirAll(audit.DefaultPath(), 0o700); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer

		if code := runDoctor(dir, &stdout); code == 0 || !strings.Contains(stdout.String(), "não consigo gravar") {
			t.Errorf("esperava falha citando a gravação, obtive código %d:\n%s", code, stdout.String())
		}
	})

	t.Run("qualquer falha sai com código diferente de 0", func(t *testing.T) {
		setHome(t, t.TempDir())
		var stdout bytes.Buffer

		code := runDoctor(t.TempDir(), &stdout) // pasta sem .claude/settings.json

		if code == 0 {
			t.Error("esperava um código de saída diferente de 0")
		}
		if !strings.Contains(stdout.String(), "FALHA") {
			t.Errorf("a saída deveria mostrar FALHA:\n%s", stdout.String())
		}
	})
}

func TestRunDispatch(t *testing.T) {
	t.Run("sem argumentos mostra o uso e sai com 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		code := run(nil, strings.NewReader(""), &stdout, &stderr)

		if code != 2 {
			t.Errorf("código de saída: esperava 2, obtive %d", code)
		}
		if !strings.Contains(stderr.String(), "uso:") {
			t.Errorf("stderr deveria mostrar o uso: %q", stderr.String())
		}
	})

	t.Run("subcomando desconhecido sai com 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		code := run([]string{"deploy"}, strings.NewReader(""), &stdout, &stderr)

		if code != 2 {
			t.Errorf("código de saída: esperava 2, obtive %d", code)
		}
		if !strings.Contains(stderr.String(), "uso:") {
			t.Errorf("stderr deveria mostrar o uso: %q", stderr.String())
		}
	})

	t.Run("hook chega na lógica do hook", func(t *testing.T) {
		setHome(t, t.TempDir()) // o hook de verdade grava no log de auditoria do usuário
		var stdout, stderr bytes.Buffer

		code := run([]string{"hook"}, openEvent(t, "git-push-force.json"), &stdout, &stderr)

		if code != 2 {
			t.Errorf("código de saída: esperava 2 (force push negado), obtive %d", code)
		}
	})

	t.Run("doctor trabalha na pasta atual", func(t *testing.T) {
		t.Chdir(t.TempDir()) // sem .claude: o doctor tem que falhar e mandar rodar o init
		var stdout, stderr bytes.Buffer

		code := run([]string{"doctor"}, strings.NewReader(""), &stdout, &stderr)

		if code == 0 {
			t.Error("esperava um código de saída diferente de 0")
		}
		if !strings.Contains(stdout.String(), "iron init") {
			t.Errorf("a saída deveria mandar rodar o iron init:\n%s", stdout.String())
		}
	})

	t.Run("init trabalha na pasta atual", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir) // o teste "entra" na pasta temporária e volta sozinho no fim
		var stdout, stderr bytes.Buffer

		code := run([]string{"init"}, strings.NewReader(""), &stdout, &stderr)

		if code != 0 {
			t.Fatalf("código de saída: esperava 0, obtive %d (stderr=%q)", code, stderr.String())
		}
		if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err != nil {
			t.Errorf("o settings.json deveria ter sido criado na pasta atual: %v", err)
		}
	})
}

func TestRunHookReadsPlanOnlyWhenNeeded(t *testing.T) {
	cases := []struct {
		command string
		reads   int
	}{
		{"git status", 0},
		{"kubectl delete namespace prod", 0},
		{"terraform plan -out=create.tfplan", 0},
		{"terraform apply -auto-approve", 0},                     // sem plano: deny sem ler nada
		{"terraform destroy", 0},                                 // deny pelo registro, antes do apply
		{"cd infra && terraform apply create.tfplan", 0},         // deny pelo cd, antes de ler
		{"git push --force && terraform apply create.tfplan", 0}, // deny do force push vence antes
		{`echo "terraform apply create.tfplan"`, 0},              // é só texto
		{"terraform apply create.tfplan", 1},                     // aqui o plano decide
		{"terraform apply create.tfplan && terraform apply create.tfplan", 2},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			reads := 0
			deps := testDeps()
			deps.readPlan = func(req tfplan.Request) ([]byte, error) {
				reads++
				return fakeReadPlan(req)
			}
			var stdout, stderr bytes.Buffer

			runHook(strings.NewReader(`{"tool_name":"Bash","cwd":"/srv/loja","tool_input":{"command":`+strconv.Quote(c.command)+`}}`), &stdout, &stderr, deps)

			if reads != c.reads {
				t.Errorf("terraform show: esperava %d chamada(s), obtive %d", c.reads, reads)
			}
		})
	}
}

func auditEvent(command string) string {
	return `{"session_id":"sessao-audit","tool_name":"Bash","cwd":"/srv/loja","tool_input":{"command":` + strconv.Quote(command) + `}}`
}

func TestRunHookWritesAuditEntry(t *testing.T) {
	cases := []struct {
		name      string
		command   string
		answer    dialog.Answer
		want      audit.Entry
		resources []audit.Resource
	}{
		{"allow", "git status", dialog.Unavailable,
			audit.Entry{Class: "git status", Decision: "allow"}, nil},
		{"deny do registro", "git push --force", dialog.Unavailable,
			audit.Entry{Class: "git push", Decision: "deny", Rule: "git-force-push"}, nil},
		{"deny do apply sem plano", "terraform apply -auto-approve", dialog.Unavailable,
			audit.Entry{Class: "terraform apply", Decision: "deny", Rule: "terraform-apply"}, nil},
		{"ask recusado na janela", "terraform apply delete.tfplan", dialog.Rejected,
			audit.Entry{Class: "terraform apply", Decision: "deny", Rule: "terraform-apply", Dialog: "rejected"},
			[]audit.Resource{{Address: "null_resource.marker[0]", Action: "delete"}}},
		{"ask aprovado na janela", "git push --force-with-lease", dialog.Approved,
			audit.Entry{Class: "git push", Decision: "userApproved", Rule: "git-force-push", Dialog: "approved"}, nil},
		{"ask sem janela", "git push --force-with-lease", dialog.Unavailable,
			audit.Entry{Class: "git push", Decision: "ask", Rule: "git-force-push", Dialog: "unavailable"}, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := &fakeAudit{}
			d := &fakeDialog{answer: c.answer}
			deps := testDeps()
			deps.audit, deps.confirm = log.append, d.confirm
			var stdout, stderr bytes.Buffer

			runHook(strings.NewReader(auditEvent(c.command)), &stdout, &stderr, deps)

			if len(log.entries) != 1 {
				t.Fatalf("esperava 1 entrada, obtive %d", len(log.entries))
			}
			got := log.entries[0]
			if got.Session != "sessao-audit" || got.Class != c.want.Class || got.Decision != c.want.Decision ||
				got.Rule != c.want.Rule || got.Dialog != c.want.Dialog {
				t.Errorf("entrada: esperava %+v, obtive %+v", c.want, got)
			}
			if !slices.Equal(got.Resources, c.resources) {
				t.Errorf("recursos: esperava %+v, obtive %+v", c.resources, got.Resources)
			}
		})
	}
}

func TestRunHookAuditsInvalidEvent(t *testing.T) {
	log := &fakeAudit{}
	deps := testDeps()
	deps.audit = log.append
	var stdout, stderr bytes.Buffer

	runHook(strings.NewReader(`{"tool_input": "segredo-123`), &stdout, &stderr, deps)

	if len(log.entries) != 1 || log.entries[0].Decision != "deny" || log.entries[0].Rule != "event" {
		t.Errorf("esperava uma entrada deny da regra event, obtive %+v", log.entries)
	}
}

func TestRunHookAuditNeverContainsSecrets(t *testing.T) {
	commands := []string{
		`git push https://u:s3cr3t@github.com/o/r.git --force`,
		`psql "postgres://app:s3cr3t@db/loja" -c "DROP TABLE users"`,
		`kubectl --token s3cr3t delete namespace app`,
		`export API_TOKEN=s3cr3t && terraform apply delete.tfplan`,
		`curl -H "Authorization: Bearer s3cr3t" https://api.exemplo.com`,
		`./deploy-s3cr3t.sh`,
	}

	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			log := &fakeAudit{}
			deps := testDeps()
			deps.audit = log.append
			var stdout, stderr bytes.Buffer

			runHook(strings.NewReader(auditEvent(command)), &stdout, &stderr, deps)

			data, err := json.Marshal(log.entries)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "s3cr3t") {
				t.Errorf("o log vazou o segredo: %s", data)
			}
		})
	}
}

func TestRunHookAuditFailureDoesNotChangeDecision(t *testing.T) {
	cases := []struct {
		command  string
		wantCode int
	}{
		{"git status", 0},
		{"git push --force", 2},
	}

	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			log := &fakeAudit{err: errors.New("disco cheio")}
			deps := testDeps()
			deps.audit = log.append
			var stdout, stderr bytes.Buffer

			code := runHook(strings.NewReader(auditEvent(c.command)), &stdout, &stderr, deps)

			if code != c.wantCode {
				t.Errorf("código: esperava %d, obtive %d", c.wantCode, code)
			}
			if !strings.Contains(stderr.String(), "log de auditoria") {
				t.Errorf("stderr deveria avisar da falha do log: %q", stderr.String())
			}
		})
	}
}

func TestRunAuditVerify(t *testing.T) {
	setHome(t, t.TempDir())
	log := audit.Log{Path: audit.DefaultPath()}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"audit", "verify"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Errorf("sem log: esperava 0, obtive %d (%s%s)", code, stdout.String(), stderr.String())
	}

	for i := range 3 {
		if err := log.Append(audit.Entry{Time: time.Now(), Class: "git status", Decision: "allow", Session: strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	stdout.Reset()
	if code := run([]string{"audit", "verify"}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "íntegra") {
		t.Errorf("log íntegro: código %d, saída %q", code, stdout.String())
	}

	// Altera a linha 2: quem denuncia é a linha 3, que guardava o hash dela.
	data, _ := os.ReadFile(log.Path)
	os.WriteFile(log.Path, []byte(strings.Replace(string(data), `"session":"1"`, `"session":"X"`, 1)), 0o600)
	stdout.Reset()
	code := run([]string{"audit", "verify"}, strings.NewReader(""), &stdout, &stderr)
	out := stdout.String()
	if code != 1 || !strings.Contains(out, "QUEBRADA na linha 3") {
		t.Errorf("log alterado: esperava código 1 e quebra na linha 3, obtive %d, %q", code, out)
	}
	if !strings.Contains(out, "confirmadas: linhas 1 a 1") || !strings.Contains(out, "suspeita: linha 2") {
		t.Errorf("a mensagem deveria apontar a linha 2 como suspeita e só a 1 como confirmada: %q", out)
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"version"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 || stdout.String() != "iron "+version+"\n" {
		t.Errorf("esperava código 0 e %q, obtive %d e %q", "iron "+version+"\n", code, stdout.String())
	}
}

func TestRunHookDeadlineDenies(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	deps := testDeps()
	deps.deadline = 50 * time.Millisecond
	deps.loadEnv = func(string) rules.Env { <-blocked; return rules.Env{} }
	var stdout, stderr bytes.Buffer

	code := runHook(strings.NewReader(auditEvent("git status")), &stdout, &stderr, deps)

	if code != 2 || !strings.Contains(stderr.String(), "tempo") {
		t.Errorf("esperava deny por tempo; código %d, stderr %q", code, stderr.String())
	}
}

func TestAuditLogFollowsPolicy(t *testing.T) {
	setHome(t, t.TempDir())

	if got := auditLog(policy.AuditLog{}); got.MaxSize != 0 || got.Keep != 0 {
		t.Errorf("sem configuração deveria deixar o padrão do audit, obtive %+v", got)
	}
	if got := auditLog(policy.AuditLog{MaxSizeMB: 20, Keep: 10}); got.MaxSize != 20<<20 || got.Keep != 10 {
		t.Errorf("esperava 20 MB e 10 arquivos, obtive %+v", got)
	}
	// Os padrões do policy (piso) e do audit precisam ser os mesmos.
	if policy.AuditDefaultMaxSizeMB<<20 != audit.DefaultMaxSize || policy.AuditDefaultKeep != audit.DefaultKeep {
		t.Error("o piso do policy.yaml e o padrão do audit divergiram")
	}
}

func TestRunHookPassesPolicyAuditConfigToTheLog(t *testing.T) {
	log := &fakeAudit{}
	deps := testDeps()
	deps.audit = log.append
	deps.loadEnv = func(cwd string) rules.Env {
		p := policy.Default()
		p.AuditLog = policy.AuditLog{MaxSizeMB: 20, Keep: 10}
		return rules.Env{Policy: p, Context: []string{cwd}}
	}
	var gotCfg policy.AuditLog
	deps.audit = func(e audit.Entry, cfg policy.AuditLog) error {
		gotCfg = cfg
		return log.append(e, cfg)
	}

	var stdout, stderr bytes.Buffer
	runHook(strings.NewReader(`{"tool_name":"Bash","cwd":"/x","tool_input":{"command":"git status"}}`), &stdout, &stderr, deps)

	if want := (policy.AuditLog{MaxSizeMB: 20, Keep: 10}); gotCfg != want {
		t.Errorf("o log deveria receber %+v, recebeu %+v", want, gotCfg)
	}
}

func TestParseAgentFlag(t *testing.T) {
	cases := []struct {
		args    []string
		want    string
		wantErr bool
	}{
		{nil, "claude", false},
		{[]string{"--agent=claude"}, "claude", false},
		{[]string{"--agent", "Claude"}, "claude", false},
		{[]string{"--agent=emacs"}, "", true},
		{[]string{"--agent"}, "", true},
		{[]string{"--outra"}, "", true},
	}
	for _, c := range cases {
		a, err := parseAgentFlag(c.args)
		if (err != nil) != c.wantErr || (err == nil && a.Name() != c.want) {
			t.Errorf("parseAgentFlag(%v) = %v, %v", c.args, a, err)
		}
	}
}

func TestRunHookUnknownAgentExits2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook", "--agent=emacs"}, strings.NewReader("{}"), &stdout, &stderr); code != 2 {
		t.Errorf("esperava 2, obtive %d (%s)", code, stderr.String())
	}
}

// noAskAgent é o Claude com a capacidade de perguntar desligada: simula Codex,
// Kiro e os outros agentes que não entendem "ask".
type noAskAgent struct{ hook.Agent }

func (noAskAgent) Name() string { return "sem-ask" }

func (a noAskAgent) Capabilities() hook.Capabilities {
	c := a.Agent.Capabilities()
	c.CanAsk = false
	return c
}

func newNoAskAgent() hook.Agent { return noAskAgent{hook.Claude} }

func TestRunHookAskBecomesDenyWhenAgentCannotAsk(t *testing.T) {
	cases := []struct {
		name     string
		answer   dialog.Answer
		wantCode int
		wantOut  string // trecho esperado no stdout (allow explícito)
		wantErr  string // trecho esperado no stderr (deny)
	}{
		// Sem janela e sem ask: deny, nunca allow.
		{"janela indisponível", dialog.Unavailable, 2, "", "não sabe perguntar"},
		{"recusado na janela", dialog.Rejected, 2, "", "recusou"},
		// A janela resolve sozinha, sem depender do ask do agente.
		{"aprovado na janela", dialog.Approved, 0, `"permissionDecision":"allow"`, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &fakeDialog{answer: c.answer}
			log := &fakeAudit{}
			deps := testDeps()
			deps.confirm, deps.audit, deps.agent = d.confirm, log.append, newNoAskAgent()
			var stdout, stderr bytes.Buffer

			code := runHook(strings.NewReader(auditEvent("git push --force-with-lease")), &stdout, &stderr, deps)

			if code != c.wantCode {
				t.Errorf("código de saída: esperava %d, obtive %d", c.wantCode, code)
			}
			if !strings.Contains(stdout.String(), c.wantOut) || (c.wantOut == "" && stdout.Len() != 0) {
				t.Errorf("stdout: esperava %q, obtive %q", c.wantOut, stdout.String())
			}
			if !strings.Contains(stderr.String(), c.wantErr) {
				t.Errorf("stderr: esperava conter %q, obtive %q", c.wantErr, stderr.String())
			}
			if len(log.entries) != 1 || log.entries[0].Agent != "sem-ask" {
				t.Errorf("o log deveria registrar o agente, obtive %+v", log.entries)
			}
		})
	}
}

func TestRunHookClaudeStillAsks(t *testing.T) {
	deps := testDeps()
	var stdout, stderr bytes.Buffer

	code := runHook(strings.NewReader(auditEvent("git push --force-with-lease")), &stdout, &stderr, deps)

	if code != 0 || !strings.Contains(stdout.String(), `"permissionDecision":"ask"`) {
		t.Errorf("o Claude entende ask: esperava ask com saída 0, obtive %d %q", code, stdout.String())
	}
}

func TestRunHookAuditsAgentName(t *testing.T) {
	log := &fakeAudit{}
	deps := testDeps()
	deps.audit = log.append
	var stdout, stderr bytes.Buffer

	runHook(strings.NewReader(auditEvent("git status")), &stdout, &stderr, deps)
	runHook(strings.NewReader(`{`), &stdout, &stderr, deps)

	if len(log.entries) != 2 || log.entries[0].Agent != "claude" || log.entries[1].Agent != "claude" {
		t.Errorf("esperava o agente claude nas duas entradas, obtive %+v", log.entries)
	}
}

func TestRunInitWritesAWSTag(t *testing.T) {
	t.Run("grava a etiqueta e preserva o hook", func(t *testing.T) {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer

		code := runInit(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr)

		if code != 0 || !strings.Contains(stdout.String(), "AWS_SDK_UA_APP_ID=iron-claude") || stderr.Len() != 0 {
			t.Fatalf("%d %q %q", code, stdout.String(), stderr.String())
		}
		data, _ := os.ReadFile(setup.SettingsPath(dir))
		if !strings.Contains(string(data), `"AWS_SDK_UA_APP_ID": "iron-claude"`) || !strings.Contains(string(data), "PreToolUse") {
			t.Errorf("settings: %s", data)
		}
		if hooks, err := setup.FindHooks(setup.SettingsPath(dir)); err != nil || len(hooks) != 1 {
			t.Errorf("o hook deve continuar achável: %v %v", hooks, err)
		}
	})

	t.Run("segunda vez diz que já estava", func(t *testing.T) {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runInit(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr)
		stdout.Reset()

		runInit(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr)

		if !strings.Contains(stdout.String(), "já estava gravada") {
			t.Errorf("%q", stdout.String())
		}
	})

	t.Run("não sobrescreve o valor da pessoa", func(t *testing.T) {
		dir := t.TempDir()
		path := setup.SettingsPath(dir)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(`{"env":{"AWS_SDK_UA_APP_ID":"meu-app"}}`), 0o644)
		var stdout, stderr bytes.Buffer

		code := runInit(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr)

		data, _ := os.ReadFile(path)
		if code != 0 || !strings.Contains(string(data), "meu-app") || strings.Contains(string(data), "iron-claude") {
			t.Errorf("o valor da pessoa deve ficar: %s", data)
		}
		if !strings.Contains(stdout.String(), `já vale "meu-app"`) {
			t.Errorf("deveria avisar do conflito: %q", stdout.String())
		}
	})

	t.Run("sem a opção não grava", func(t *testing.T) {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runInit(dir, testExe, initOptions{}, &stdout, &stderr)
		data, _ := os.ReadFile(setup.SettingsPath(dir))
		if strings.Contains(string(data), "AWS_SDK_UA_APP_ID") || strings.Contains(stdout.String(), "etiqueta") {
			t.Errorf("não era para gravar: %s %q", data, stdout.String())
		}
	})

	t.Run("env inválido vira aviso e o hook é instalado", func(t *testing.T) {
		dir := t.TempDir()
		path := setup.SettingsPath(dir)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(`{"env": 5}`), 0o644)
		var stdout, stderr bytes.Buffer

		code := runInit(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr)

		if code != 0 || !strings.Contains(stderr.String(), "aviso") {
			t.Errorf("%d %q", code, stderr.String())
		}
	})
}

func TestAWSAlertsPrintsTheTemplate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"aws-alerts"}, strings.NewReader(""), &stdout, &stderr)

	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("%d %q", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "AWSTemplateFormatVersion") || !strings.Contains(stdout.String(), "AWS API Call via CloudTrail") {
		t.Errorf("saída: %.80q", stdout.String())
	}
}

func TestRunInitWarnsWhenTheBinaryIsRenamed(t *testing.T) {
	for exe, wantWarning := range map[string]bool{
		"/opt/iron":                   false,
		"/opt/iron.exe":               false,
		"/opt/iron_linux_amd64":       true,
		"/opt/iron_darwin_arm64":      true,
		`/opt/iron_windows_amd64.exe`: true,
	} {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer

		code := runInit(dir, exe, initOptions{}, &stdout, &stderr)

		if code != 0 {
			t.Fatalf("%s: código %d", exe, code)
		}
		if warned := strings.Contains(stderr.String(), "só o reconhece se o arquivo se chamar iron"); warned != wantWarning {
			t.Errorf("%s: avisou=%v, esperava %v (%q)", exe, warned, wantWarning, stderr.String())
		}
	}
}
