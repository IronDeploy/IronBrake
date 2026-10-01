package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/rules"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/watch"
)

const scriptDenyJSON = "#!/bin/sh\necho '{\"decision\":\"deny\",\"reason\":\"x\"}'\nexit 0\n" // o hook do Iron Brake no Antigravity

func installedAgy(t *testing.T, script string) (dir, iron string) {
	t.Helper()
	dir = t.TempDir()
	iron = newFakeIron(t, dir, script, 0o755)
	if _, err := setup.InstallAntigravity(dir, iron); err != nil {
		t.Fatal(err)
	}
	return dir, iron
}

func agyHooksFile(dir string) string { return filepath.Join(dir, ".agents/hooks.json") }

// Ordem: hook, binário, resposta, prazo, programas, log, limites, versão.
func TestRunAntigravityAllOK(t *testing.T) {
	dir, _ := installedAgy(t, scriptDenyJSON)
	results := RunAntigravity(dir, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, true, true, true, true, true, true, true, true)
	if !strings.Contains(results[6].Detail, "dangerously-skip-permissions") {
		t.Errorf("os limites deveriam citar a aprovação automática: %q", results[6].Detail)
	}
}

func TestRunAntigravityExit2AlsoCounts(t *testing.T) {
	dir, _ := installedAgy(t, scriptDeny)
	assertOK(t, RunAntigravity(dir, testVersion, testTimeout, kiroExtra(dir)), true, true, true, true, true, true, true, true)
}

func TestRunAntigravityNotInstalled(t *testing.T) {
	dir := t.TempDir()
	results := RunAntigravity(dir, testVersion, testTimeout, kiroExtra(dir))
	assertOK(t, results, false, false, false, false, true, true, true, true)
	if !strings.Contains(results[0].Fix, "iron init --agent=antigravity") {
		t.Errorf("deveria mandar rodar o init: %q", results[0].Fix)
	}
}

// Falha aberta silenciosa: o agy ignora um hooks.json inválido e o comando passa.
func TestRunAntigravityBrokenJSONFailsLoudly(t *testing.T) {
	dir, _ := installedAgy(t, scriptDenyJSON)
	if err := os.WriteFile(agyHooksFile(dir), []byte(`{ "iron-brake": `), 0o644); err != nil {
		t.Fatal(err)
	}
	results := RunAntigravity(dir, testVersion, testTimeout, kiroExtra(dir))
	if results[0].OK || !strings.Contains(results[0].Detail, "SEM avisar") {
		t.Errorf("deveria explicar que o agy ignora o arquivo: %+v", results[0])
	}
}

func TestRunAntigravityDisabledOrWrongMatcher(t *testing.T) {
	cases := map[string]struct{ from, to, want string }{
		"desligado":      {`{`, `{"enabled": false,`, "enabled:false"},
		"matcher errado": {`"matcher": "run_command"`, `"matcher": "view_file"`, "não pega"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir, _ := installedAgy(t, scriptDenyJSON)
			data, _ := os.ReadFile(agyHooksFile(dir))
			text := string(data)
			if name == "desligado" {
				text = strings.Replace(text, `"iron-brake": {`, `"iron-brake": {"enabled": false,`, 1)
			} else {
				text = strings.Replace(text, c.from, c.to, 1)
			}
			if err := os.WriteFile(agyHooksFile(dir), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			results := RunAntigravity(dir, testVersion, testTimeout, kiroExtra(dir))
			if results[0].OK || !strings.Contains(results[0].Detail, c.want) {
				t.Errorf("deveria falhar citando %q: %+v", c.want, results[0])
			}
		})
	}
}

func TestRunAntigravityTimeout(t *testing.T) {
	cases := map[string]struct{ replace, want string }{
		"sem prazo": {`"x": 1`, "30 s"},
		"curto":     {`"timeout": 60`, "60 s"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir, _ := installedAgy(t, scriptDenyJSON)
			data, _ := os.ReadFile(agyHooksFile(dir))
			if err := os.WriteFile(agyHooksFile(dir), []byte(strings.Replace(string(data), `"timeout": 600`, c.replace, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			results := RunAntigravity(dir, testVersion, testTimeout, kiroExtra(dir))
			if results[3].OK || !strings.Contains(results[3].Detail, c.want) || results[3].Fix == "" {
				t.Errorf("prazo curto/ausente deveria falhar: %+v", results[3])
			}
		})
	}
}

func TestRunAntigravityHookDoesNotBlock(t *testing.T) {
	for name, script := range map[string]string{"libera tudo": scriptAllow, "saída vazia": "#!/bin/sh\necho '{}'\nexit 0\n"} {
		t.Run(name, func(t *testing.T) {
			dir, _ := installedAgy(t, script)
			assertOK(t, RunAntigravity(dir, testVersion, testTimeout, kiroExtra(dir)), true, true, false, true, true, true, true, true)
		})
	}
}

func TestRunAntigravityBinaryMissing(t *testing.T) {
	dir, iron := installedAgy(t, scriptDenyJSON)
	if err := os.Remove(iron); err != nil {
		t.Fatal(err)
	}
	assertOK(t, RunAntigravity(dir, testVersion, testTimeout, kiroExtra(dir)), true, false, false, true, true, true, true, true)
}

func TestRunAntigravityCoverage(t *testing.T) {
	dir, _ := installedAgy(t, scriptDenyJSON)
	brain := t.TempDir()
	session := "22d4da59-96e7-4ba2-b985-74477ca3618e"
	logDir := filepath.Join(brain, session, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	step := func(n int, kind string, when time.Time, extra string) string {
		return fmt.Sprintf(`{"step_index":%d,"type":%q,"status":"DONE","created_at":%q%s}`, n, kind, when.Format("2006-01-02T15:04:05Z"), extra)
	}
	transcript := strings.Join([]string{
		step(1, "PLANNER_RESPONSE", at, fmt.Sprintf(`,"tool_calls":[{"name":"run_command","args":{"CommandLine":"git push --force origin main","Cwd":%q}}]`, dir)),
		step(2, "GENERIC", at.Add(2*time.Second), ""),
		step(3, "PLANNER_RESPONSE", at.Add(3*time.Second), ""),
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	extra := kiroExtra(dir)
	extra.Coverage = &Coverage{
		Dirs: []string{brain}, Since: time.Now().Add(-time.Hour),
		Source: watch.Source{
			Classify: rules.Classify, Entries: func(time.Time) ([]audit.Entry, error) { return nil, nil },
			Format: &watch.AntigravityFormat, Keep: watch.InProject(dir),
		},
	}
	results := RunAntigravity(dir, testVersion, testTimeout, extra)

	if len(results) != 9 || results[6].Name != nameCoverage || results[6].OK || !strings.Contains(results[6].Detail, "SEM decisão") {
		t.Errorf("um comando sem decisão deveria falhar a cobertura: %+v", results)
	}
}
