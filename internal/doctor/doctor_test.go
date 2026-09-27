package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	scriptDeny  = "#!/bin/sh\necho bloqueado >&2\nexit 2\n" // se comporta como o Iron Brake
	scriptAllow = "#!/bin/sh\nexit 0\n"                     // libera tudo
	scriptError = "#!/bin/sh\nexit 1\n"                     // erro comum: não bloqueia
	scriptHangs = "#!/bin/sh\nexec sleep 5\n"               // trava (exec: o sleep vira o processo)
	// testTimeout é folgado: com a máquina ocupada (go test ./... roda os
	// pacotes em paralelo), iniciar o script sh já passou de 1s e o teste
	// falhava à toa. Só o caso que TESTA travamento usa hangTimeout.
	testTimeout  = 10 * time.Second
	hangTimeout  = time.Second
	testVersion  = "1.2.3"
	fixIronInit  = "iron init"
	fixRebuild   = "go build"
	settingsFile = "settings.json"
)

func newFakeIron(t *testing.T, dir, script string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(dir, "bin", "iron")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), mode); err != nil {
		t.Fatal(err)
	}

	return path
}

func settingsWith(t *testing.T, matcher string, commands ...string) string {
	t.Helper()

	var groups []any
	for _, command := range commands {
		groups = append(groups, map[string]any{
			"matcher": matcher,
			"hooks":   []any{map[string]any{"type": "command", "command": command, "args": []any{"hook"}}},
		})
	}

	data, err := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": groups}})
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func writeSettings(t *testing.T, dir, content string) string {
	t.Helper()

	path := filepath.Join(dir, ".claude", settingsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func assertOK(t *testing.T, results []Result, want ...bool) {
	t.Helper()

	if len(results) != len(want) {
		t.Fatalf("esperava %d verificações, obtive %d", len(want), len(results))
	}
	for i, r := range results {
		if r.OK != want[i] {
			t.Errorf("verificação %d (%s): esperava OK=%v, obtive OK=%v (%s)", i+1, r.Name, want[i], r.OK, r.Detail)
		}
		if !r.OK && r.Fix == "" {
			t.Errorf("verificação %d (%s) falhou sem dizer como corrigir", i+1, r.Name)
		}
	}
}

func TestRunAllOK(t *testing.T) {
	dir := t.TempDir()
	iron := newFakeIron(t, dir, scriptDeny, 0o755)
	path := writeSettings(t, dir, settingsWith(t, "Bash", iron))

	results := Run(path, testVersion, testTimeout)

	assertOK(t, results, true, true, true, true, true)
	if results[4].Detail != testVersion {
		t.Errorf("a verificação 5 deveria mostrar a versão %q, obtive %q", testVersion, results[4].Detail)
	}
	if !AllOK(results) {
		t.Error("AllOK deveria ser true")
	}
}

func TestRunMissingSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", settingsFile)

	results := Run(path, testVersion, testTimeout)

	assertOK(t, results, false, false, false, false, true)
	if !strings.Contains(results[0].Fix, fixIronInit) {
		t.Errorf("a correção deveria citar %q: %q", fixIronInit, results[0].Fix)
	}
	if AllOK(results) {
		t.Error("AllOK deveria ser false")
	}
}

func TestRunWrongPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nao", "existe", "iron")
	path := writeSettings(t, dir, settingsWith(t, "Bash", missing))

	results := Run(path, testVersion, testTimeout)

	assertOK(t, results, true, false, false, true, true)
	if !strings.Contains(results[1].Detail, missing) {
		t.Errorf("a mensagem deveria citar o caminho %q: %q", missing, results[1].Detail)
	}
	if !strings.Contains(results[1].Fix, fixRebuild) || !strings.Contains(results[1].Fix, fixIronInit) {
		t.Errorf("a correção deveria citar %q e %q: %q", fixRebuild, fixIronInit, results[1].Fix)
	}
}

func TestRunHookRemoved(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"arquivo sem hooks", `{}`},
		{"só hook de outra ferramenta", `{"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"/x/fmt.sh"}]}]}}`},
		{"hook do iron no matcher errado", ""}, // preenchido no laço
	}
	cases[2].content = settingsWith(t, "Write", "/x/iron")

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeSettings(t, t.TempDir(), c.content)

			results := Run(path, testVersion, testTimeout)

			assertOK(t, results, false, false, false, false, true)
			if !strings.Contains(results[0].Fix, fixIronInit) {
				t.Errorf("a correção deveria citar %q: %q", fixIronInit, results[0].Fix)
			}
		})
	}
}

func TestRunInvalidSettings(t *testing.T) {
	path := writeSettings(t, t.TempDir(), `{"hooks": `)

	results := Run(path, testVersion, testTimeout)

	assertOK(t, results, false, false, false, false, true)
}

func TestRunBinaryNotExecutable(t *testing.T) {
	dir := t.TempDir()
	iron := newFakeIron(t, dir, scriptDeny, 0o644)
	path := writeSettings(t, dir, settingsWith(t, "Bash", iron))

	results := Run(path, testVersion, testTimeout)

	assertOK(t, results, true, false, false, true, true)
}

func TestRunHookDoesNotBlock(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{"libera tudo (exit 0)", scriptAllow},
		{"erro comum (exit 1)", scriptError},
		{"trava (timeout)", scriptHangs},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			iron := newFakeIron(t, dir, c.script, 0o755)
			path := writeSettings(t, dir, settingsWith(t, "Bash", iron))

			timeout := testTimeout
			if c.script == scriptHangs {
				timeout = hangTimeout // o script dorme 5s: estoura 1s de propósito
			}
			results := Run(path, testVersion, timeout)

			assertOK(t, results, true, true, false, true, true)
		})
	}
}

func TestRunDoesNotExecuteRelativePath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Se este script rodar, ele deixa o arquivo "ran" na pasta atual.
	newFakeIron(t, dir, "#!/bin/sh\ntouch ran\nexit 2\n", 0o755)
	path := writeSettings(t, dir, settingsWith(t, "Bash", "./bin/iron"))

	results := Run(path, testVersion, testTimeout)

	assertOK(t, results, true, false, false, true, true)
	if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
		t.Error("o doctor executou o binário de caminho relativo")
	}
}

func TestRunPrefersWorkingHookOverStaleOne(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "old", "iron")
	iron := newFakeIron(t, dir, scriptDeny, 0o755)
	path := writeSettings(t, dir, settingsWith(t, "Bash", stale, iron))

	results := Run(path, testVersion, testTimeout)

	assertOK(t, results, true, true, true, true, true)
}

func TestPrint(t *testing.T) {
	results := []Result{
		{Name: "primeira", OK: true, Detail: "tudo certo aqui"},
		{Name: "segunda", OK: false, Detail: "algo quebrou", Fix: "conserte assim"},
	}
	var out bytes.Buffer

	Print(&out, results)

	for _, want := range []string{"OK", "primeira", "tudo certo aqui", "FALHA", "segunda", "algo quebrou", "conserte assim"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("a saída deveria conter %q:\n%s", want, out.String())
		}
	}
}

func settingsWithTimeout(t *testing.T, command string, timeout float64) string {
	t.Helper()

	data, err := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "Bash",
		"hooks":   []any{map[string]any{"type": "command", "command": command, "args": []any{"hook"}, "timeout": timeout}},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Um timeout do hook menor que o prazo do Iron Brake faz o Claude Code
// desistir antes do deny por tempo, e o comando passa.
func TestRunHookTimeout(t *testing.T) {
	cases := []struct {
		name    string
		timeout float64
		wantOK  bool
		detail  string
	}{
		{"curto demais", 30, false, "30 s"},
		{"no limite", 570, true, "570 s"},
		{"padrão explícito", 600, true, "600 s"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			iron := newFakeIron(t, dir, scriptDeny, 0o755)
			path := writeSettings(t, dir, settingsWithTimeout(t, iron, c.timeout))

			results := Run(path, testVersion, testTimeout)

			assertOK(t, results, true, true, true, c.wantOK, true)
			if !strings.Contains(results[3].Detail, c.detail) {
				t.Errorf("detalhe: esperava conter %q, obtive %q", c.detail, results[3].Detail)
			}
		})
	}
}

func TestRunHookTimeoutDefault(t *testing.T) {
	dir := t.TempDir()
	iron := newFakeIron(t, dir, scriptDeny, 0o755)
	path := writeSettings(t, dir, settingsWith(t, "Bash", iron))

	results := Run(path, testVersion, testTimeout)

	if !results[3].OK || !strings.Contains(results[3].Detail, "padrão") {
		t.Errorf("sem timeout no settings.json deveria passar citando o padrão; obtive %+v", results[3])
	}
}
