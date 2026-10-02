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

// codexPayload monta o stdin no formato real do Codex CLI.
func codexPayload(t *testing.T, tool, command string) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"session_id": "01a0f9b2-1dc5-7573-972a-ca24ca6b643d", "turn_id": "t1", "cwd": "/home/dev/projeto",
		"hook_event_name": "PreToolUse", "model": "gpt-6-luna", "permission_mode": "default",
		"tool_name": tool, "tool_input": map[string]any{"command": command}, "tool_use_id": "exec-1",
		"transcript_path": "/home/dev/.codex/sessions/x.jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func codexDeps(d *fakeDialog, log *fakeAudit) hookDeps {
	deps := testDeps()
	deps.agent = hook.Codex
	deps.confirm, deps.audit = d.confirm, log.append
	return deps
}

func TestRunHookCodexBlocksAndAllows(t *testing.T) {
	t.Run("bloqueia force push com exit 2 e o motivo no stderr", func(t *testing.T) {
		log := &fakeAudit{}
		var stdout, stderr bytes.Buffer
		code := runHook(codexPayload(t, "Bash", "git push --force origin main"), &stdout, &stderr, codexDeps(&fakeDialog{}, log))
		if code != 2 || !strings.Contains(stderr.String(), "force push") || stdout.Len() != 0 {
			t.Errorf("código %d, stderr %q, stdout %q", code, stderr.String(), stdout.String())
		}
		if len(log.entries) != 1 || log.entries[0].Agent != "codex" || log.entries[0].Session != "01a0f9b2-1dc5-7573-972a-ca24ca6b643d" {
			t.Errorf("o log deveria trazer agent=codex e a sessão: %+v", log.entries)
		}
	})

	t.Run("comando seguro: silêncio total (JSON allow seria falha do hook)", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(codexPayload(t, "Bash", "git status"), &stdout, &stderr, codexDeps(&fakeDialog{}, &fakeAudit{}))
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("allow deveria ser silencioso: %d %q %q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("apply_patch não é shell: o texto do patch não é analisado", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(codexPayload(t, "apply_patch", "*** Begin Patch\n*** Add File: x\n+git push --force origin main\n*** End Patch"), &stdout, &stderr, codexDeps(&fakeDialog{}, &fakeAudit{}))
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("o patch deveria passar em silêncio: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
}

// O Codex trata o "ask" como falha do hook e executa o comando: o Iron Brake
// pergunta na janela e, sem janela, bloqueia.
func TestRunHookCodexAskNeedsTheWindow(t *testing.T) {
	cases := []struct {
		name         string
		answer       dialog.Answer
		wantCode     int
		wantInStderr string
	}{
		{"janela indisponível", dialog.Unavailable, 2, "não sabe perguntar"},
		{"recusado", dialog.Rejected, 2, "recusou"},
		{"aprovado: sem saída, o comando executa", dialog.Approved, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := &fakeAudit{}
			var stdout, stderr bytes.Buffer
			code := runHook(codexPayload(t, "Bash", "git push --force-with-lease"), &stdout, &stderr, codexDeps(&fakeDialog{answer: c.answer}, log))
			if code != c.wantCode || stdout.Len() != 0 || !strings.Contains(stderr.String(), c.wantInStderr) {
				t.Errorf("código %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
			if len(log.entries) != 1 || log.entries[0].Dialog != string(c.answer) {
				t.Errorf("o log deveria registrar a janela: %+v", log.entries)
			}
		})
	}
}

func TestRunHookCodexBrokenInputBlocks(t *testing.T) {
	for name, in := range map[string]string{"vazio": "", "quebrado": "{"} {
		var stdout, stderr bytes.Buffer
		if code := runHook(strings.NewReader(in), &stdout, &stderr, codexDeps(&fakeDialog{}, &fakeAudit{})); code != 2 {
			t.Errorf("%s deveria bloquear (exit 2): %d", name, code)
		}
	}
}

func TestRunInitCodex(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := runInitCodex(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "instalado") {
		t.Fatalf("código %d: %q %q", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, ".codex/hooks.json"))
	if err != nil || !strings.Contains(string(data), "hook --agent=codex") || !strings.Contains(string(data), `"^Bash$"`) {
		t.Errorf("hooks.json deveria existir e chamar o iron: %v", err)
	}
	if !strings.Contains(stdout.String(), "NÃO roda este hook até você confiar") || !strings.Contains(stdout.String(), "/hooks") {
		t.Errorf("deveria explicar a confiança: %q", stdout.String())
	}
	cfg, err := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	if err != nil || !strings.Contains(string(cfg), `AWS_SDK_UA_APP_ID = "iron-codex"`) || !strings.Contains(stdout.String(), "etiqueta da AWS") {
		t.Errorf("deveria gravar a etiqueta: %v %q", err, cfg)
	}
	for _, other := range []string{".claude", ".kiro", ".agents"} {
		if _, err := os.Stat(filepath.Join(dir, other)); err == nil {
			t.Errorf("não deve criar %s", other)
		}
	}

	// segunda vez: nada muda, e não repete o aviso de confiança (o hook é o mesmo).
	stdout.Reset()
	if code := runInitCodex(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "já estava") || strings.Contains(stdout.String(), "NÃO roda") {
		t.Errorf("segunda vez: %d %q", code, stdout.String())
	}
	// caminho novo: o hook muda e a confiança é pedida de novo.
	stdout.Reset()
	if code := runInitCodex(dir, "/outro/iron", initOptions{}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "atualizado") || !strings.Contains(stdout.String(), "NÃO roda") {
		t.Errorf("caminho novo: %d %q", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "AWS_SDK_UA_APP_ID") {
		t.Errorf("com --no-aws-tag não deveria falar da etiqueta: %q", stdout.String())
	}
}

func TestRunInitCodexTagWithExistingConfigAndHarden(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex/config.toml"), []byte("model = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	runInitCodex(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "[shell_environment_policy]") {
		t.Errorf("com config.toml existente deveria mostrar o trecho a acrescentar: %q", stdout.String())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".codex/config.toml")); string(data) != "model = \"x\"\n" {
		t.Errorf("não pode editar o TOML da pessoa: %q", data)
	}

	if code := runInitCodex(t.TempDir(), testExe, initOptions{harden: true}, &stdout, &stderr); code != 2 {
		t.Errorf("--harden deveria sair 2, obtive %d", code)
	}
}

func TestCodexConfigPath(t *testing.T) {
	t.Setenv("CODEX_HOME", "/x/codex-home")
	if got := codexConfigPath(); got != filepath.Join("/x/codex-home", "config.toml") {
		t.Errorf("CODEX_HOME deveria valer: %q", got)
	}
	t.Setenv("CODEX_HOME", "")
	if got := codexConfigPath(); !strings.HasSuffix(got, filepath.Join(".codex", "config.toml")) {
		t.Errorf("sem CODEX_HOME vale ~/.codex: %q", got)
	}
}
