package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/watch"
)

const agyWatchSession = "22d4da59-96e7-4ba2-b985-74477ca3618e"

func agyStepLine(step int, kind string, at time.Time, calls ...string) string {
	line := `{"step_index":` + strconv.Itoa(step) + `,"source":"MODEL","type":"` + kind + `","status":"DONE","created_at":"` + at.UTC().Format("2006-01-02T15:04:05Z") + `"`
	if len(calls) > 0 {
		var tcs []string
		for _, c := range calls {
			cmd, cwd, _ := strings.Cut(c, "|")
			tcs = append(tcs, `{"name":"run_command","args":{"CommandLine":"`+cmd+`","Cwd":"`+cwd+`"}}`)
		}
		line += `,"tool_calls":[` + strings.Join(tcs, ",") + `]`
	}
	return line + "}"
}

// agyWatchHome cria o ~/.gemini/antigravity-cli/brain com uma conversa.
func agyWatchHome(t *testing.T, lines ...string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(watch.BrainDir(home), agyWatchSession, ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript_full.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestParseWatchArgsAgent(t *testing.T) {
	if def, _ := parseWatchArgs(nil); def.agent != "claude" {
		t.Errorf("o padrão é claude: %q", def.agent)
	}
	for _, args := range [][]string{{"--agent", "antigravity"}, {"--agent=agy"}} {
		if got, err := parseWatchArgs(args); err != nil || got.agent != "antigravity" {
			t.Errorf("%v: %+v %v", args, got, err)
		}
	}
	for _, bad := range [][]string{{"--agent", "kiro"}, {"--agent=emacs"}, {"--agent"}} {
		if _, err := parseWatchArgs(bad); err == nil {
			t.Errorf("%v deveria dar erro", bad)
		}
	}
}

func TestWatchOnceAntigravity(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	project := "/home/dev/proj"
	home := agyWatchHome(t,
		agyStepLine(1, "PLANNER_RESPONSE", now.Add(-10*time.Minute), "git status|"+project),
		agyStepLine(2, "GENERIC", now.Add(-10*time.Minute+2*time.Second)),
		agyStepLine(3, "PLANNER_RESPONSE", now.Add(-5*time.Minute), "git push --force origin main|"+project+"/sub"),
		agyStepLine(4, "GENERIC", now.Add(-5*time.Minute+2*time.Second)),
		agyStepLine(5, "PLANNER_RESPONSE", now.Add(-4*time.Minute), "git push --force origin main|/outro/projeto"),
		agyStepLine(6, "GENERIC", now.Add(-4*time.Minute+2*time.Second)),
		agyStepLine(7, "PLANNER_RESPONSE", now.Add(-4*time.Minute+4*time.Second)),
	)
	ok := audit.Entry{Time: now.Add(-10*time.Minute + time.Second), Session: agyWatchSession, Class: "git status", Decision: "allow", Agent: "antigravity"}

	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--agent=antigravity"}, &stdout, &stderr, watchDeps{home: home, cwd: project, now: time.Now, entries: entriesOf(ok)})

	if code != 1 || !strings.Contains(stdout.String(), "SEM DECISÃO: git push") || !strings.Contains(stdout.String(), "2 comando(s)") {
		t.Errorf("deveria acusar só o force push deste projeto (2 comandos conferidos):\n%d %s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "--force") {
		t.Errorf("a saída nunca mostra o comando: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "não está em") || !strings.Contains(stderr.String(), "iron init --agent=antigravity") {
		t.Errorf("sem hooks.json, deveria avisar: %q", stderr.String())
	}

	// --all: sem filtro de projeto, o comando do outro projeto também é conferido.
	stdout.Reset()
	runWatch([]string{"--once", "--agent=antigravity", "--all"}, &stdout, &stderr, watchDeps{home: home, cwd: project, now: time.Now, entries: entriesOf(ok)})
	if !strings.Contains(stdout.String(), "3 comando(s)") || !strings.Contains(stdout.String(), "2 SEM decisão") {
		t.Errorf("com --all são 3 comandos e 2 sem decisão:\n%s", stdout.String())
	}
}

func TestWatchAntigravityMissingBrainDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--agent=antigravity"}, &stdout, &stderr, watchDeps{home: t.TempDir(), cwd: "/p", now: time.Now, entries: entriesOf()})
	if code != 1 || !strings.Contains(stderr.String(), "conversas do Antigravity") {
		t.Errorf("%d %q", code, stderr.String())
	}
}
