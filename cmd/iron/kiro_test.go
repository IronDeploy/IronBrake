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

// kiroPayload monta o stdin no formato real de cada engine do Kiro CLI.
func kiroPayload(t *testing.T, engine, command string) *strings.Reader {
	t.Helper()
	m := map[string]any{"cwd": "/home/dev/projeto"}
	switch engine {
	case "v2":
		m["hook_event_name"], m["tool_name"], m["session_id"] = "preToolUse", "shell", "e075b302-baae-491a-b9f0-6a936d898630"
		m["tool_input"] = map[string]any{"command": command, "__tool_use_purpose": "x"}
	case "v3":
		m["hook_event_name"], m["tool_name"], m["session_id"] = "PreToolUse", "execute_bash", "sess_34ca342c-4ef7-4a12-9467-b5d874a946a1"
		m["tool_input"] = map[string]any{"command": command, "description": nil, "cwd": nil, "run_in_background": false, "timeout": nil}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func kiroDeps(d *fakeDialog, log *fakeAudit) hookDeps {
	deps := testDeps()
	deps.agent = hook.Kiro
	deps.confirm, deps.audit = d.confirm, log.append
	return deps
}

func TestRunHookKiroBlocksAndAllows(t *testing.T) {
	for _, engine := range []string{"v2", "v3"} {
		t.Run(engine+" bloqueia force push", func(t *testing.T) {
			log := &fakeAudit{}
			var stdout, stderr bytes.Buffer
			code := runHook(kiroPayload(t, engine, "git push --force origin main"), &stdout, &stderr, kiroDeps(&fakeDialog{}, log))

			if code != 2 || stderr.Len() == 0 || stdout.Len() != 0 {
				t.Errorf("deveria sair 2 com o motivo no stderr e stdout vazio: %d %q %q", code, stderr.String(), stdout.String())
			}
			if len(log.entries) != 1 || log.entries[0].Agent != "kiro" || log.entries[0].Session == "" {
				t.Errorf("o log deveria trazer agent=kiro e a sessão: %+v", log.entries)
			}
		})

		t.Run(engine+" libera comando seguro em silêncio", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runHook(kiroPayload(t, engine, "git status"), &stdout, &stderr, kiroDeps(&fakeDialog{}, &fakeAudit{}))
			if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("allow deveria ser silencioso: %d %q %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

// O Kiro ignora o JSON de ask: sem janela, o ask vira deny (nunca allow).
func TestRunHookKiroAskNeedsTheWindow(t *testing.T) {
	cases := []struct {
		name     string
		answer   dialog.Answer
		wantCode int
		wantErr  string
	}{
		{"janela indisponível", dialog.Unavailable, 2, "não sabe perguntar"},
		{"recusado", dialog.Rejected, 2, "recusou"},
		{"aprovado", dialog.Approved, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := &fakeAudit{}
			var stdout, stderr bytes.Buffer
			code := runHook(kiroPayload(t, "v3", "git push --force-with-lease"), &stdout, &stderr, kiroDeps(&fakeDialog{answer: c.answer}, log))

			if code != c.wantCode || stdout.Len() != 0 || !strings.Contains(stderr.String(), c.wantErr) {
				t.Errorf("código %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
			if len(log.entries) != 1 || log.entries[0].Dialog != string(c.answer) {
				t.Errorf("o log deveria registrar a resposta da janela: %+v", log.entries)
			}
		})
	}
}

func TestRunHookKiroBrokenInputBlocks(t *testing.T) {
	for name, in := range map[string]string{"vazio (Kiro IDE)": "", "quebrado": "{"} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runHook(strings.NewReader(in), &stdout, &stderr, kiroDeps(&fakeDialog{}, &fakeAudit{})); code != 2 {
				t.Errorf("esperava 2, obtive %d", code)
			}
		})
	}
}

func TestRunHookKiroViaFlag(t *testing.T) {
	setHome(t, t.TempDir()) // o hook de verdade grava no log de auditoria do usuário
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook", "--agent=kiro"}, strings.NewReader("{"), &stdout, &stderr); code != 2 {
		t.Errorf("esperava 2, obtive %d (%s)", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "desconhecido") {
		t.Errorf("kiro deveria ser um agente conhecido: %q", stderr.String())
	}
}

func TestRunInitKiro(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := runInitKiro(dir, testExe, initOptions{}, &stdout, &stderr); code != 0 {
		t.Fatalf("código %d (%s)", code, stderr.String())
	}
	for _, f := range []string{".kiro/agents/kiro_default.json", ".kiro/hooks/iron-brake.json"} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || !strings.Contains(string(data), "hook --agent=kiro") {
			t.Errorf("%s deveria existir e chamar o iron: %v", f, err)
		}
	}
	if !strings.Contains(stdout.String(), "--no-interactive") {
		t.Errorf("deveria avisar do modo não interativo do v3: %q", stdout.String())
	}
	// o Claude não é tocado.
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Error("init --agent=kiro não deve criar .claude")
	}

	stdout.Reset()
	if code := runInitKiro(dir, testExe, initOptions{}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "já estava") {
		t.Errorf("segunda vez deveria dizer que já estava: %d %q", code, stdout.String())
	}
}

func TestRunInitKiroRefusesHarden(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runInitKiro(t.TempDir(), testExe, initOptions{harden: true}, &stdout, &stderr); code != 2 {
		t.Errorf("esperava 2, obtive %d", code)
	}
}

func TestInitAgentFlag(t *testing.T) {
	if a, err := initAgent(nil); err != nil || a.Name() != "claude" {
		t.Errorf("padrão deveria ser claude: %v %v", a, err)
	}
	for _, args := range [][]string{{"--agent=kiro"}, {"--harden", "--agent", "kiro"}} {
		if a, err := initAgent(args); err != nil || a.Name() != "kiro" {
			t.Errorf("%v: %v %v", args, a, err)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init", "--agent=emacs"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Errorf("agente desconhecido no init deveria sair 2, obtive %d", code)
	}
}
