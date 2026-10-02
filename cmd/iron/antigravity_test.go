package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/dialog"
	"github.com/IronDeploy/IronBrake/internal/hook"
)

// agyPayload monta o stdin no formato real do Antigravity CLI.
func agyPayload(t *testing.T, tool, command string) *strings.Reader {
	t.Helper()
	args := map[string]any{"toolAction": "x"}
	if command != "" {
		args["CommandLine"], args["Cwd"] = command, "/home/dev/projeto"
	} else {
		args["AbsolutePath"] = "/home/dev/projeto/a.txt"
	}
	data, err := json.Marshal(map[string]any{
		"toolCall": map[string]any{"name": tool, "args": args}, "stepIdx": 2,
		"conversationId": "99436074-2c53-4c19-a411-c0e716b9f2b9",
		"workspacePaths": []string{"/home/dev/projeto"}, "modelName": "gemini-3.8-flash-high",
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func agyDeps(d *fakeDialog, log *fakeAudit) hookDeps {
	deps := testDeps()
	deps.agent = hook.Antigravity
	deps.confirm, deps.audit = d.confirm, log.append
	return deps
}

func decisionOf(t *testing.T, stdout string) (decision, reason string) {
	t.Helper()
	var out struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout deveria ser JSON: %q (%v)", stdout, err)
	}
	return out.Decision, out.Reason
}

func TestRunHookAntigravityBlocksAndAllows(t *testing.T) {
	t.Run("bloqueia force push com deny em JSON", func(t *testing.T) {
		log := &fakeAudit{}
		var stdout, stderr bytes.Buffer
		code := runHook(agyPayload(t, "run_command", "git push --force origin main"), &stdout, &stderr, agyDeps(&fakeDialog{}, log))

		decision, reason := decisionOf(t, stdout.String())
		if code != 0 || decision != "deny" || !strings.Contains(reason, "force push") || stderr.Len() != 0 {
			t.Errorf("código %d, decisão %q, motivo %q, stderr %q", code, decision, reason, stderr.String())
		}
		if len(log.entries) != 1 || log.entries[0].Agent != "antigravity" || log.entries[0].Session != "99436074-2c53-4c19-a411-c0e716b9f2b9" {
			t.Errorf("o log deveria trazer agent=antigravity e a conversa: %+v", log.entries)
		}
	})

	t.Run("comando seguro: silêncio total (um {} bloquearia)", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(agyPayload(t, "run_command", "git status"), &stdout, &stderr, agyDeps(&fakeDialog{}, &fakeAudit{}))
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("allow deveria ser silencioso: %d %q %q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("outra ferramenta passa em silêncio", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(agyPayload(t, "view_file", ""), &stdout, &stderr, agyDeps(&fakeDialog{}, &fakeAudit{}))
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("view_file deveria passar em silêncio: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
}

// O ask do hook não segura nada com a aprovação automática do agy ligada: o
// Iron Brake pergunta na janela e, sem janela, bloqueia.
func TestRunHookAntigravityAskNeedsTheWindow(t *testing.T) {
	cases := []struct {
		name         string
		answer       dialog.Answer
		wantDeny     bool
		wantInReason string
	}{
		{"janela indisponível", dialog.Unavailable, true, "não sabe perguntar"},
		{"recusado", dialog.Rejected, true, "recusou"},
		{"aprovado: sem saída, o agente decide pela permissão dele", dialog.Approved, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := &fakeAudit{}
			var stdout, stderr bytes.Buffer
			code := runHook(agyPayload(t, "run_command", "git push --force-with-lease"), &stdout, &stderr, agyDeps(&fakeDialog{answer: c.answer}, log))

			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("código %d, stderr %q", code, stderr.String())
			}
			if c.wantDeny {
				if d, reason := decisionOf(t, stdout.String()); d != "deny" || !strings.Contains(reason, c.wantInReason) {
					t.Errorf("deveria bloquear com %q: %q", c.wantInReason, stdout.String())
				}
			} else if stdout.Len() != 0 {
				t.Errorf("aprovado deveria sair em silêncio: %q", stdout.String())
			}
			if len(log.entries) != 1 || log.entries[0].Dialog != string(c.answer) {
				t.Errorf("o log deveria registrar a janela: %+v", log.entries)
			}
		})
	}
}

func TestRunHookAntigravityBrokenInputBlocks(t *testing.T) {
	for name, in := range map[string]string{"vazio": "", "quebrado": "{"} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runHook(strings.NewReader(in), &stdout, &stderr, agyDeps(&fakeDialog{}, &fakeAudit{}))
			if d, _ := decisionOf(t, stdout.String()); code != 0 || d != "deny" {
				t.Errorf("deveria bloquear: %d %q", code, stdout.String())
			}
		})
	}
}

func TestRunHookAntigravityViaFlagAndAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // o hook de verdade grava no log de auditoria do usuário
	for _, flag := range []string{"--agent=antigravity", "--agent=agy"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"hook", flag}, strings.NewReader("{"), &stdout, &stderr)
		if d, _ := decisionOf(t, stdout.String()); code != 0 || d != "deny" || strings.Contains(stderr.String(), "desconhecido") {
			t.Errorf("%s: %d %q %q", flag, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunInitAntigravity(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := runInitAntigravity(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "instalado") {
		t.Fatalf("código %d: %q %q", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, ".agents/hooks.json"))
	if err != nil || !strings.Contains(string(data), "hook --agent=antigravity") {
		t.Errorf("hooks.json deveria existir e chamar o iron: %v", err)
	}
	if !strings.Contains(stdout.String(), "confiou") {
		t.Errorf("deveria avisar da confiança na pasta: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "AWS_SDK_UA_APP_ID=iron-antigravity") {
		t.Errorf("deveria dizer como etiquetar o agente na AWS: %q", stdout.String())
	}
	for _, other := range []string{".claude", ".kiro"} {
		if _, err := os.Stat(filepath.Join(dir, other)); err == nil {
			t.Errorf("não deve criar %s", other)
		}
	}

	stdout.Reset()
	if code := runInitAntigravity(dir, testExe, initOptions{}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "já estava") {
		t.Errorf("segunda vez: %d %q", code, stdout.String())
	}
	stdout.Reset()
	if code := runInitAntigravity(dir, "/outro/iron", initOptions{}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "atualizado") {
		t.Errorf("caminho novo: %d %q", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "AWS_SDK_UA_APP_ID") {
		t.Errorf("com --no-aws-tag não deveria falar da etiqueta: %q", stdout.String())
	}
}

func TestRunInitAntigravityRefusesHardenAndBrokenFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runInitAntigravity(t.TempDir(), testExe, initOptions{harden: true}, &stdout, &stderr); code != 2 {
		t.Errorf("--harden deveria sair 2, obtive %d", code)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agents/hooks.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := runInitAntigravity(dir, testExe, initOptions{}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "hooks.json") {
		t.Errorf("arquivo quebrado deveria falhar citando-o: %d %q", code, stderr.String())
	}
}
