package doctor

import (
	"os"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/setup"
)

func installedCursor(t *testing.T, script string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	iron := newFakeIron(t, dir, script, 0o755)
	if _, err := setup.InstallCursor(dir, iron); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Ordem: hook, binário, resposta, prazo, failClosed, programas, log, limites, versão.
func TestRunCursorAllOK(t *testing.T) {
	dir := installedCursor(t, scriptDeny)
	results := RunCursor(dir, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, true, true, true, true, true, true, true, true, true)
	if !strings.Contains(results[7].Detail, "ask nativo") {
		t.Errorf("os limites deveriam citar o ask: %q", results[7].Detail)
	}
}

func TestRunCursorNotInstalled(t *testing.T) {
	dir := t.TempDir()
	results := RunCursor(dir, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, false, false, false, false, false, true, true, true, true)
	if !strings.Contains(results[0].Fix, "iron init --agent=cursor") {
		t.Errorf("deveria mandar rodar o init: %q", results[0].Fix)
	}
}

func TestRunCursorTimeoutAndFailClosed(t *testing.T) {
	dir := installedCursor(t, scriptDeny)
	path := setup.CursorHooksPath(dir)
	data, _ := os.ReadFile(path)
	write := func(old, new string) {
		t.Helper()
		if !strings.Contains(string(data), old) {
			t.Fatalf("o arquivo não tem %q:\n%s", old, data)
		}
		os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644)
	}

	write(`"timeout": 600`, `"x": 1`)
	if r := RunCursor(dir, testVersion, testTimeout, kiroExtra(dir))[3]; r.OK || !strings.Contains(r.Detail, "60 s") || r.Fix == "" {
		t.Errorf("sem timeout deveria falhar: %+v", r)
	}
	write(`"timeout": 600`, `"timeout": 60`)
	if r := RunCursor(dir, testVersion, testTimeout, kiroExtra(dir))[3]; r.OK {
		t.Errorf("60 s deveria falhar: %+v", r)
	}
	write(`"failClosed": true`, `"failClosed": false`)
	r := RunCursor(dir, testVersion, testTimeout, kiroExtra(dir))
	if r[4].OK || !strings.Contains(r[4].Fix, "iron init --agent=cursor") {
		t.Errorf("sem failClosed deveria falhar: %+v", r[4])
	}
	// só o prazo e o failClosed falham: o hook continua respondendo.
	if !r[0].OK || !r[2].OK {
		t.Errorf("só o prazo e o failClosed deveriam falhar: %+v", r)
	}
}

func TestRunCursorHookDoesNotBlock(t *testing.T) {
	dir := installedCursor(t, scriptAllow)
	if r := RunCursor(dir, testVersion, testTimeout, kiroExtra(dir))[2]; r.OK {
		t.Errorf("um hook que libera o force push deveria reprovar: %+v", r)
	}
}

func TestCursorBlocked(t *testing.T) {
	cases := []struct {
		code   int
		stdout string
		want   bool
	}{
		{0, `{"permission":"deny","user_message":"x"}`, true},
		{2, "", true},
		{0, `{"permission":"allow"}`, false},
		{0, "", false},
		{1, "", false}, // exit 1 só bloqueia com failClosed: não conta como prova.
		{0, "isto não é json", false},
	}
	for _, c := range cases {
		if got, _ := cursorBlocked(c.code, c.stdout); got != c.want {
			t.Errorf("cursorBlocked(%d, %q) = %v", c.code, c.stdout, got)
		}
	}
}
