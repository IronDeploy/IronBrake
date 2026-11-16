package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/dialog"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

// cursorPayload monta o stdin do beforeShellExecution no formato real do Cursor
// ("cwd" vem vazio; a pasta está em workspace_roots).
func cursorPayload(t *testing.T, command string) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"conversation_id": "aada8690-ff44-472c-a78c-d195fb3d1555", "model": "default", "command": command, "cwd": "",
		"sandbox": true, "session_id": "aada8690-ff44-472c-a78c-d195fb3d1555", "hook_event_name": "beforeShellExecution",
		"cursor_version": "3.30.14", "workspace_roots": []string{"/home/dev/projeto"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func cursorDeps(d *fakeDialog, log *fakeAudit) hookDeps {
	deps := testDeps()
	deps.agent = hook.Cursor
	deps.confirm, deps.audit = d.confirm, log.append
	return deps
}

func cursorPermission(t *testing.T, stdout string) string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("saída não é JSON: %v (%q)", err, stdout)
	}
	return out["permission"]
}

func TestRunHookCursorBlocksAndAllows(t *testing.T) {
	t.Run("bloqueia force push com JSON deny", func(t *testing.T) {
		log := &fakeAudit{}
		var stdout, stderr bytes.Buffer
		code := runHook(cursorPayload(t, "git push --force origin main"), &stdout, &stderr, cursorDeps(&fakeDialog{}, log))
		if code != 0 || cursorPermission(t, stdout.String()) != "deny" || !strings.Contains(stdout.String(), "force push") {
			t.Errorf("código %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
		}
		if len(log.entries) != 1 || log.entries[0].Agent != "cursor" || log.entries[0].Session != "aada8690-ff44-472c-a78c-d195fb3d1555" {
			t.Errorf("o log deveria trazer agent=cursor e a sessão: %+v", log.entries)
		}
	})

	// com failClosed, saída vazia bloqueia: o allow tem de ser explícito.
	t.Run("comando seguro: allow explícito", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(cursorPayload(t, "git status"), &stdout, &stderr, cursorDeps(&fakeDialog{}, &fakeAudit{}))
		if code != 0 || cursorPermission(t, stdout.String()) != "allow" || stderr.Len() != 0 {
			t.Errorf("allow deveria ser JSON explícito: %d %q %q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("payload quebrado bloqueia, nunca libera", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(strings.NewReader("{"), &stdout, &stderr, cursorDeps(&fakeDialog{}, &fakeAudit{}))
		if !(code == 2 || (code == 0 && cursorPermission(t, stdout.String()) == "deny")) {
			t.Errorf("deveria bloquear: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
}

func TestRunHookCursorAskNeedsTheWindow(t *testing.T) {
	cmd := "git reset --hard"
	t.Run("janela aprova: allow", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		d := &fakeDialog{answer: dialog.Approved}
		code := runHook(cursorPayload(t, cmd), &stdout, &stderr, cursorDeps(d, &fakeAudit{}))
		if code != 0 || cursorPermission(t, stdout.String()) != "allow" {
			t.Errorf("aprovado deveria liberar: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("janela nega: deny", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		d := &fakeDialog{answer: dialog.Rejected}
		code := runHook(cursorPayload(t, cmd), &stdout, &stderr, cursorDeps(d, &fakeAudit{}))
		if code != 0 || cursorPermission(t, stdout.String()) != "deny" {
			t.Errorf("negado deveria bloquear: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
	// o ask nativo do Cursor é aprovado pelo --force: sem janela, nunca vira allow.
	t.Run("janela indisponível: deny", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		d := &fakeDialog{answer: dialog.Unavailable}
		code := runHook(cursorPayload(t, cmd), &stdout, &stderr, cursorDeps(d, &fakeAudit{}))
		if code != 0 || cursorPermission(t, stdout.String()) != "deny" {
			t.Errorf("sem janela deveria bloquear: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
}

func TestRunInitCursor(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runInitCursor(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "instalado") {
		t.Fatalf("código %d: %q %q", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(setup.CursorHooksPath(dir))
	if err != nil || !strings.Contains(string(data), "hook --agent=cursor") || !strings.Contains(string(data), `"failClosed": true`) {
		t.Errorf("hooks.json deveria chamar o iron com failClosed: %v\n%s", err, data)
	}
	if !strings.Contains(stdout.String(), "iron doctor --agent=cursor") || !strings.Contains(stdout.String(), "AWS_SDK_UA_APP_ID=iron-cursor") {
		t.Errorf("deveria apontar o doctor e a etiqueta da AWS: %q", stdout.String())
	}

	stdout.Reset()
	if code := runInitCursor(dir, testExe, initOptions{}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "já estava") || strings.Contains(stdout.String(), "AWS_SDK") {
		t.Errorf("segunda vez: %d %q", code, stdout.String())
	}
	stdout.Reset()
	if code := runInitCursor(dir, "/outro/iron", initOptions{}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "atualizado") {
		t.Errorf("caminho novo: %d %q", code, stdout.String())
	}
}

func TestRunInitCursorHardenIsClaudeOnly(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runInitCursor(dir, testExe, initOptions{harden: true}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "--harden") {
		t.Errorf("deveria recusar --harden: %d %q", code, stderr.String())
	}
	if _, err := os.Stat(setup.CursorHooksPath(dir)); err == nil {
		t.Error("não deveria instalar nada")
	}
}

// cursorShapedPreToolUse é o que o Cursor manda ao hook do Claude que ele importa
// de .claude/settings.json (capturado com o cursor-agent 2026.11.13).
func cursorShapedPreToolUse(dir, command string) string {
	data, _ := json.Marshal(map[string]any{
		"conversation_id": "c1", "model": "default", "tool_name": "Shell",
		"tool_input": map[string]any{"command": command, "cwd": ""}, "cwd": "",
		"session_id": "c1", "hook_event_name": "preToolUse", "cursor_version": "2026.11.13-eacade1",
		"workspace_roots": []string{dir},
	})
	return string(data)
}

// Com o hook do Cursor na pasta, o hook do Claude importado pelo Cursor cede:
// senão seriam duas janelas e duas linhas no log para o mesmo comando.
func TestClaudeHookStandsDownUnderCursor(t *testing.T) {
	dir := t.TempDir()
	if _, err := setup.InstallCursor(dir, testExe); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook"}, strings.NewReader(cursorShapedPreToolUse(dir, "git push --force origin main")), &stdout, &stderr); code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("deveria ceder em silêncio: %d %q %q", code, stdout.String(), stderr.String())
	}
}

// Sem o hook do Cursor, o do Claude segue analisando o comando como antes.
func TestClaudeHookStillAnalyzesCursorPayloadWithoutCursorHook(t *testing.T) {
	isolateCopilotHome(t) // o run grava no log de auditoria do HOME
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"hook"}, strings.NewReader(cursorShapedPreToolUse(dir, "git push --force origin main")), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "force push") {
		t.Errorf("sem o hook do Cursor deveria bloquear: %d %q %q", code, stdout.String(), stderr.String())
	}
}

// O Claude Code de verdade (hook "PreToolUse", sem cursor_version) nunca cede.
func TestClaudeHookDoesNotStandDownForClaudeCode(t *testing.T) {
	isolateCopilotHome(t) // o run grava no log de auditoria do HOME
	dir := t.TempDir()
	if _, err := setup.InstallCursor(dir, testExe); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{
		"tool_name": "Bash", "tool_input": map[string]any{"command": "git push --force origin main"},
		"hook_event_name": "PreToolUse", "cwd": dir, "workspace_roots": []string{dir},
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook"}, strings.NewReader(string(data)), &stdout, &stderr); code != 2 {
		t.Errorf("o Claude Code deveria ser analisado: %d %q %q", code, stdout.String(), stderr.String())
	}
}
