package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/setup"
)

// installedCodex instala o hook e grava um config.toml do usuário que confia na
// pasta e no hook, como o Codex faria.
func installedCodex(t *testing.T, script string) (dir, iron, cfg string) {
	t.Helper()
	dir = t.TempDir()
	iron = newFakeIron(t, dir, script, 0o755)
	if _, err := setup.InstallCodex(dir, iron); err != nil {
		t.Fatal(err)
	}
	cfg = filepath.Join(t.TempDir(), "config.toml")
	trustAll(t, dir, cfg)
	return dir, iron, cfg
}

func trustAll(t *testing.T, dir, cfg string) {
	t.Helper()
	body := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n\n[hooks.state]\n\n[hooks.state.%q]\ntrusted_hash = \"sha256:abc\"\n",
		dir, setup.CodexHooksPath(dir)+":pre_tool_use:0:0")
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// o config.toml é gravado depois do hooks.json, como quando se confia no hook.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(cfg, later, later); err != nil {
		t.Fatal(err)
	}
}

// Ordem: hook, binário, resposta, prazo, confiança, programas, log, limites, versão.
func TestRunCodexAllOK(t *testing.T) {
	dir, _, cfg := installedCodex(t, scriptDeny)
	results := RunCodex(dir, cfg, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, true, true, true, true, true, true, true, true, true)
	if !strings.Contains(results[7].Detail, "LIBERA") {
		t.Errorf("os limites deveriam avisar da falha aberta: %q", results[7].Detail)
	}
}

func TestRunCodexNotInstalled(t *testing.T) {
	dir := t.TempDir()
	results := RunCodex(dir, filepath.Join(dir, "config.toml"), testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, false, false, false, false, false, true, true, true, true)
	if !strings.Contains(results[0].Fix, "iron init --agent=codex") {
		t.Errorf("deveria mandar rodar o init: %q", results[0].Fix)
	}
}

func TestRunCodexTrustFailures(t *testing.T) {
	cases := map[string]struct {
		config string // "" = sem arquivo
		want   string
	}{
		"sem config.toml":      {"", "não está confiada"},
		"pasta não confiada":   {"[tui]\nx = 1\n", "não está confiada"},
		"hook não confiado":    {"PROJECT", "ainda não foi confiado"},
		"hooks.json mais novo": {"STALE", "alterado depois"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir, _, cfg := installedCodex(t, scriptDeny)
			switch c.config {
			case "":
				os.Remove(cfg)
			case "PROJECT":
				body := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir)
				os.WriteFile(cfg, []byte(body), 0o644)
			case "STALE":
				past := time.Now().Add(-time.Hour)
				os.Chtimes(cfg, past, past) // o hooks.json (agora) é mais novo que a confiança
			default:
				os.WriteFile(cfg, []byte(c.config), 0o644)
			}
			results := RunCodex(dir, cfg, testVersion, testTimeout, kiroExtra(dir))
			if results[4].OK || !strings.Contains(results[4].Detail, c.want) || results[4].Fix == "" {
				t.Errorf("deveria falhar a confiança citando %q: %+v", c.want, results[4])
			}
			// as outras verificações do hook continuam valendo.
			if !results[0].OK || !results[2].OK {
				t.Errorf("só a confiança deveria falhar: %+v", results)
			}
		})
	}
}

func TestRunCodexTimeout(t *testing.T) {
	dir, _, cfg := installedCodex(t, scriptDeny)
	path := setup.CodexHooksPath(dir)
	data, _ := os.ReadFile(path)

	// sem o campo: o padrão do Codex (600 s) serve.
	os.WriteFile(path, []byte(strings.Replace(string(data), `"timeout": 600`, `"x": 1`, 1)), 0o644)
	if r := RunCodex(dir, cfg, testVersion, testTimeout, kiroExtra(dir))[3]; !r.OK {
		t.Errorf("sem campo deveria passar: %+v", r)
	}
	// curto: o Codex mata o hook e libera o comando.
	os.WriteFile(path, []byte(strings.Replace(string(data), `"timeout": 600`, `"timeout": 30`, 1)), 0o644)
	if r := RunCodex(dir, cfg, testVersion, testTimeout, kiroExtra(dir))[3]; r.OK || !strings.Contains(r.Detail, "LIBERA") || r.Fix == "" {
		t.Errorf("30 s deveria falhar avisando que libera o comando: %+v", r)
	}
}

func TestRunCodexMatcherDoesNotCoverBash(t *testing.T) {
	dir, _, cfg := installedCodex(t, scriptDeny)
	path := setup.CodexHooksPath(dir)
	data, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(data), `"matcher": "^Bash$"`, `"matcher": "^apply_patch$"`, 1)), 0o644)
	r := RunCodex(dir, cfg, testVersion, testTimeout, kiroExtra(dir))[0]
	if r.OK || !strings.Contains(r.Detail, "não pega") {
		t.Errorf("matcher que não pega o Bash deveria falhar: %+v", r)
	}
}

func TestRunCodexHookDoesNotBlock(t *testing.T) {
	// exit 1 e saída vazia liberam o comando no Codex: o doctor exige o exit 2.
	for name, script := range map[string]string{"libera tudo": scriptAllow, "erro comum (exit 1)": scriptError} {
		t.Run(name, func(t *testing.T) {
			dir, _, cfg := installedCodex(t, script)
			assertOK(t, RunCodex(dir, cfg, testVersion, testTimeout, kiroExtra(dir)), true, true, false, true, true, true, true, true, true)
		})
	}
}

func TestRunCodexBinaryMissing(t *testing.T) {
	dir, iron, cfg := installedCodex(t, scriptDeny)
	os.Remove(iron)
	assertOK(t, RunCodex(dir, cfg, testVersion, testTimeout, kiroExtra(dir)), true, false, false, true, true, true, true, true, true)
}
