package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/setup"
)

// installedCopilot instala o hook e grava um config.json do usuário que confia
// na pasta, como a opção "remember this folder" do Copilot.
func installedCopilot(t *testing.T, script string) (dir, iron, cfg string) {
	t.Helper()
	dir = t.TempDir()
	iron = newFakeIron(t, dir, script, 0o755)
	if _, err := setup.InstallCopilot(dir, iron); err != nil {
		t.Fatal(err)
	}
	cfg = filepath.Join(t.TempDir(), "config.json")
	body := fmt.Sprintf("// This file is managed automatically.\n{\"trustedFolders\":[%q]}\n", dir)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, iron, cfg
}

// Ordem: hook, binário, resposta, prazo, confiança, programas, log, limites, versão.
func TestRunCopilotAllOK(t *testing.T) {
	dir, _, cfg := installedCopilot(t, scriptDeny)
	results := RunCopilot(dir, cfg, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, true, true, true, true, true, true, true, true, true)
	if !strings.Contains(results[7].Detail, "LIBERA") {
		t.Errorf("os limites deveriam avisar do prazo: %q", results[7].Detail)
	}
}

func TestRunCopilotNotInstalled(t *testing.T) {
	dir := t.TempDir()
	results := RunCopilot(dir, filepath.Join(dir, "config.json"), testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, false, false, false, false, false, true, true, true, true)
	if !strings.Contains(results[0].Fix, "iron init --agent=copilot") {
		t.Errorf("deveria mandar rodar o init: %q", results[0].Fix)
	}
}

func TestRunCopilotTrustFailures(t *testing.T) {
	for name, setupCfg := range map[string]func(cfg string){
		"sem config.json": func(cfg string) { os.Remove(cfg) },
		"outra pasta":     func(cfg string) { os.WriteFile(cfg, []byte(`{"trustedFolders":["/outra"]}`), 0o644) },
		"config ilegível": func(cfg string) { os.WriteFile(cfg, []byte(`{`), 0o644) },
	} {
		t.Run(name, func(t *testing.T) {
			dir, _, cfg := installedCopilot(t, scriptDeny)
			setupCfg(cfg)
			r := RunCopilot(dir, cfg, testVersion, testTimeout, kiroExtra(dir))
			if r[4].OK || r[4].Fix == "" {
				t.Errorf("deveria falhar a confiança com o que fazer: %+v", r[4])
			}
			if name != "config ilegível" && !strings.Contains(r[4].Fix, "remember this folder") {
				t.Errorf("deveria citar a opção certa do Copilot: %q", r[4].Fix)
			}
			// as outras verificações do hook continuam valendo.
			if !r[0].OK || !r[2].OK {
				t.Errorf("só a confiança deveria falhar: %+v", r)
			}
		})
	}
}

func TestRunCopilotTimeout(t *testing.T) {
	dir, _, cfg := installedCopilot(t, scriptDeny)
	path := setup.CopilotHooksPath(dir)
	data, _ := os.ReadFile(path)

	// sem o campo vale o padrão do Copilot (30 s): estoura e libera o comando.
	os.WriteFile(path, []byte(strings.Replace(string(data), `"timeoutSec": 600`, `"x": 1`, 1)), 0o644)
	if r := RunCopilot(dir, cfg, testVersion, testTimeout, kiroExtra(dir))[3]; r.OK || !strings.Contains(r.Detail, "LIBERA") || r.Fix == "" {
		t.Errorf("sem timeoutSec deveria falhar: %+v", r)
	}
	os.WriteFile(path, []byte(strings.Replace(string(data), `"timeoutSec": 600`, `"timeoutSec": 30`, 1)), 0o644)
	if r := RunCopilot(dir, cfg, testVersion, testTimeout, kiroExtra(dir))[3]; r.OK || !strings.Contains(r.Detail, "LIBERA") {
		t.Errorf("30 s deveria falhar: %+v", r)
	}
}

func TestRunCopilotHookDoesNotBlock(t *testing.T) {
	for name, script := range map[string]string{"libera tudo": scriptAllow} {
		t.Run(name, func(t *testing.T) {
			dir, _, cfg := installedCopilot(t, script)
			assertOK(t, RunCopilot(dir, cfg, testVersion, testTimeout, kiroExtra(dir)), true, true, false, true, true, true, true, true, true)
		})
	}
}

func TestRunCopilotBinaryMissing(t *testing.T) {
	dir, iron, cfg := installedCopilot(t, scriptDeny)
	os.Remove(iron)
	assertOK(t, RunCopilot(dir, cfg, testVersion, testTimeout, kiroExtra(dir)), true, false, false, true, true, true, true, true, true)
}
