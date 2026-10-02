package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

func kiroExtra(dir string) Extra {
	return Extra{Log: audit.Log{Path: filepath.Join(dir, "audit.log")}, ConfigPath: filepath.Join(dir, "config.yaml"), ProjectDir: dir}
}

func installedKiro(t *testing.T, script string) (dir, iron string) {
	t.Helper()
	dir = t.TempDir()
	iron = newFakeIron(t, dir, script, 0o755)
	if _, err := setup.InstallKiro(dir, iron); err != nil {
		t.Fatal(err)
	}
	return dir, iron
}

// Ordem: hooks, binário, resposta, prazos, programas, log, limites, versão.
func TestRunKiroAllOK(t *testing.T) {
	dir, _ := installedKiro(t, scriptDeny)
	results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))

	assertOK(t, results, true, true, true, true, true, true, true, true)
	if !strings.Contains(results[6].Detail, "--no-interactive") {
		t.Errorf("deveria avisar do v3 não interativo: %q", results[6].Detail)
	}
}

func TestRunKiroNotInstalled(t *testing.T) {
	dir := t.TempDir()
	results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, false, false, false, false, true, true, true, true)
	if !strings.Contains(results[0].Fix, "iron init --agent=kiro") {
		t.Errorf("deveria mandar rodar o init: %q", results[0].Fix)
	}
}

func TestRunKiroMissingOneEngine(t *testing.T) {
	cases := map[string]struct{ remove, wantDetail string }{
		"sem v3": {".kiro/hooks/iron-brake.json", "Kiro v3"},
		"sem v2": {".kiro/agents/kiro_default.json", "Kiro v2"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir, _ := installedKiro(t, scriptDeny)
			if err := os.Remove(filepath.Join(dir, c.remove)); err != nil {
				t.Fatal(err)
			}
			results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))
			if results[0].OK || !strings.Contains(results[0].Detail, c.wantDetail) {
				t.Errorf("deveria falhar citando %q: %+v", c.wantDetail, results[0])
			}
		})
	}
}

func TestRunKiroMissingTimeoutFails(t *testing.T) {
	dir, _ := installedKiro(t, scriptDeny)
	path := filepath.Join(dir, ".kiro/agents/kiro_default.json")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), `"timeout_ms": 600000`, `"x": 1`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))
	if results[3].OK || !strings.Contains(results[3].Detail, "sem prazo") || results[3].Fix == "" {
		t.Errorf("prazo ausente deveria falhar: %+v", results[3])
	}
}

func TestRunKiroShortTimeoutFails(t *testing.T) {
	dir, _ := installedKiro(t, scriptDeny)
	path := filepath.Join(dir, ".kiro/hooks/iron-brake.json")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), `"timeout": 600`, `"timeout": 30`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))
	if results[3].OK || !strings.Contains(results[3].Detail, "30 s") {
		t.Errorf("prazo curto deveria falhar: %+v", results[3])
	}
}

func TestRunKiroHookDoesNotBlock(t *testing.T) {
	dir, _ := installedKiro(t, scriptAllow)
	results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, true, true, false, true, true, true, true, true)
}

func TestRunKiroBinaryMissing(t *testing.T) {
	dir, iron := installedKiro(t, scriptDeny)
	if err := os.Remove(iron); err != nil {
		t.Fatal(err)
	}
	results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, true, false, false, true, true, true, true, true)
}

func TestRunKiroWarnsAboutDoubleExecution(t *testing.T) {
	dir, iron := installedKiro(t, scriptDeny)
	cmd := `'` + iron + `' hook --agent=kiro`
	quoted, _ := json.Marshal(cmd) // o caminho do Windows tem "\\", que no JSON precisa de escape
	upgraded := `{"name":"meu","hooks":[{"name":"x","trigger":"preToolUse","matcher":".*","action":{"type":"command","command":` + string(quoted) + `},"timeout":600}]}`
	if err := os.WriteFile(filepath.Join(dir, ".kiro/agents/meu.json"), []byte(upgraded), 0o644); err != nil {
		t.Fatal(err)
	}
	results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))
	if !strings.Contains(results[6].Detail, "meu.json") || !strings.Contains(results[6].Detail, "duas vezes") {
		t.Errorf("deveria avisar da execução em dobro: %q", results[6].Detail)
	}
}

func TestRunKiroBrokenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".kiro/agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kiro/agents/x.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	results := RunKiro(dir, testVersion, testTimeout, kiroExtra(dir))
	if results[0].OK || !strings.Contains(results[0].Detail, "x.json") {
		t.Errorf("deveria citar o arquivo quebrado: %+v", results[0])
	}
}
