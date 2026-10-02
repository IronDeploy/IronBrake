package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/rules"
	"github.com/IronDeploy/IronBrake/internal/watch"
)

func noDecisions(time.Time) ([]audit.Entry, error) { return nil, nil }

// Cobertura do Kiro: um comando executado sem decisão (v3 não interativo) falha.
func TestRunKiroCoverage(t *testing.T) {
	dir, _ := installedKiro(t, scriptDeny)
	sessions := t.TempDir()
	session := "sess_379f8601-8fb7-45f9-b0d1-20257a458405"
	sdir := filepath.Join(sessions, "8e890988228762f5", session)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "session.json"), []byte(fmt.Sprintf(`{"workspacePaths":[%q]}`, dir)), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute).UTC()
	iso := func(d time.Duration) string { return at.Add(d).Format("2006-01-02T15:04:05.000Z") }
	lines := strings.Join([]string{
		`{"timestamp":"` + iso(0) + `","payload":{"type":"tool_call","toolCallId":"a","toolName":"execute_bash","args":{"command":"git push --force origin main","cwd":null},"status":"completed"}}`,
		`{"timestamp":"` + iso(time.Second) + `","payload":{"type":"tool_result","toolCallId":"a","content":"{}","success":true}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sdir, "messages.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	extra := kiroExtra(dir)
	extra.Coverage = &Coverage{
		Dirs: []string{sessions}, Since: time.Now().Add(-time.Hour),
		Source: watch.Source{Classify: rules.Classify, Entries: noDecisions, Format: &watch.KiroFormat, Keep: watch.InProject(dir)},
	}
	results := RunKiro(dir, testVersion, testTimeout, extra)

	if len(results) != 9 || results[6].Name != nameCoverage || results[6].OK || !strings.Contains(results[6].Detail, "SEM decisão") {
		t.Errorf("um comando sem decisão deveria falhar a cobertura: %+v", results)
	}
}

// Cobertura do Codex: hook instalado mas não confiado = comando sem decisão.
func TestRunCodexCoverage(t *testing.T) {
	dir, _, cfg := installedCodex(t, scriptDeny)
	sessions := t.TempDir()
	session := "01a0f9b2-1dc5-7573-972a-ca24ca6b643d"
	rollout := filepath.Join(sessions, "2026", "10", "01", "rollout-2026-10-01T19-59-54-"+session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-5 * time.Minute).UTC()
	iso := func(d time.Duration) string { return at.Add(d).Format("2006-01-02T15:04:05.000Z") }
	script := strconv.Quote(`const r = await tools.exec_command({cmd:"git push --force origin main"});`)
	lines := strings.Join([]string{
		`{"timestamp":"` + iso(0) + `","type":"session_meta","payload":{"id":"` + session + `","cwd":` + strconv.Quote(dir) + `}}`,
		`{"timestamp":"` + iso(time.Second) + `","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"exec","input":` + script + `}}`,
		`{"timestamp":"` + iso(2*time.Second) + `","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":[]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(rollout, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	extra := kiroExtra(dir)
	extra.Coverage = &Coverage{
		Dirs: []string{sessions}, Since: time.Now().Add(-time.Hour),
		Source: watch.Source{Classify: rules.Classify, Entries: noDecisions, Format: &watch.CodexFormat, Keep: watch.InProject(dir)},
	}
	results := RunCodex(dir, cfg, testVersion, testTimeout, extra)

	if len(results) != 10 || results[7].Name != nameCoverage || results[7].OK || !strings.Contains(results[7].Detail, "SEM decisão") {
		t.Errorf("um comando sem decisão deveria falhar a cobertura: %+v", results)
	}
}
