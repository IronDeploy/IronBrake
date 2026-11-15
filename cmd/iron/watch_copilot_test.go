package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"strings"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/watch"
)

const copilotSess = "05641926-7973-460d-935d-0a7a90c69f2a"

func copilotRow(kind, data string, at time.Time) string {
	return `{"type":"` + kind + `","data":` + data + `,"id":"x","timestamp":"` + iso(at) + `"}`
}

func copilotStartRow(id, command string, at time.Time) string {
	return copilotRow("tool.execution_start", `{"toolCallId":"`+id+`","toolName":"bash","arguments":{"command":"`+command+`"}}`, at)
}

func copilotCompleteRow(id string, at time.Time) string {
	return copilotRow("tool.execution_complete", `{"toolCallId":"`+id+`","success":true}`, at)
}

func TestWatchOnceCopilot(t *testing.T) {
	now := time.Now()
	project := "/home/dev/proj"
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", "")
	dir := filepath.Join(watch.CopilotSessionsDir(home), copilotSess)
	writeAt(t, filepath.Join(dir, "workspace.yaml"), "id: "+copilotSess, "cwd: "+project)
	writeAt(t, filepath.Join(dir, "events.jsonl"),
		copilotStartRow("c1", "git status", now.Add(-10*time.Minute)), copilotCompleteRow("c1", now.Add(-10*time.Minute+time.Second)),
		copilotStartRow("c2", "git push --force origin main", now.Add(-5*time.Minute)), copilotCompleteRow("c2", now.Add(-5*time.Minute+time.Second)),
	)
	ok := audit.Entry{Time: now.Add(-10*time.Minute + 300*time.Millisecond), Session: copilotSess, Class: "git status", Decision: "allow", Agent: "copilot"}

	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--agent=copilot"}, &stdout, &stderr, watchDeps{home: home, cwd: project, now: time.Now, entries: entriesOf(ok)})
	if code != 1 || !strings.Contains(stdout.String(), "SEM DECISÃO: git push") || !strings.Contains(stdout.String(), "2 comando(s)") || strings.Contains(stdout.String(), "--force") {
		t.Errorf("deveria acusar só o force push sem decisão (e nunca mostrar o comando):\n%d %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "iron init --agent=copilot") {
		t.Errorf("sem o hook na pasta, deveria avisar: %q", stderr.String())
	}
}

// Com o hook instalado na pasta não há aviso, e o que o Copilot recusou não é acusado.
func TestWatchOnceCopilotWithHookAndBlockedCall(t *testing.T) {
	now := time.Now()
	project := t.TempDir()
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", "")
	if _, err := setup.InstallCopilot(project, testExe); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(watch.CopilotSessionsDir(home), copilotSess)
	writeAt(t, filepath.Join(dir, "workspace.yaml"), "id: "+copilotSess, "cwd: "+project)
	denied := copilotRow("tool.execution_complete", `{"toolCallId":"c1","success":false,"error":{"code":"denied"}}`, now.Add(-10*time.Minute+time.Second))
	writeAt(t, filepath.Join(dir, "events.jsonl"), copilotStartRow("c1", "git push --force origin main", now.Add(-10*time.Minute)), denied)

	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--agent=copilot"}, &stdout, &stderr, watchDeps{home: home, cwd: project, now: time.Now, entries: entriesOf()})
	if code != 0 || strings.Contains(stdout.String(), "SEM DECISÃO") || stderr.Len() != 0 {
		t.Errorf("comando recusado pelo agente não é lacuna, e com o hook não há aviso:\n%d %q %q", code, stdout.String(), stderr.String())
	}
}

func TestWatchCopilotMissingSessionsDir(t *testing.T) {
	t.Setenv("COPILOT_HOME", "")
	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--agent=copilot"}, &stdout, &stderr, watchDeps{home: t.TempDir(), cwd: "/p", now: time.Now, entries: entriesOf()})
	if code != 1 || !strings.Contains(stderr.String(), "as sessões do Copilot") || !strings.Contains(stderr.String(), "--dir") {
		t.Errorf("%d %q", code, stderr.String())
	}
}
