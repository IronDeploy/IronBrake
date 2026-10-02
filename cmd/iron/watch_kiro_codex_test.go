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

const (
	kiroSess  = "sess_379f8601-8fb7-45f9-b0d1-20257a458405"
	codexSess = "01a0f9b2-1dc5-7573-972a-ca24ca6b643d"
)

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func writeAt(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func kiroV3Line(kind, id, command string, at time.Time) string {
	if kind == "tool_result" {
		return `{"timestamp":"` + iso(at) + `","payload":{"type":"tool_result","toolCallId":"` + id + `","content":"{}","success":true}}`
	}
	return `{"timestamp":"` + iso(at) + `","payload":{"type":"tool_call","toolCallId":"` + id + `","toolName":"execute_bash","args":{"command":"` + command + `","cwd":null},"status":"completed"}}`
}

func TestWatchOnceKiro(t *testing.T) {
	now := time.Now()
	project := "/home/dev/proj"
	home := t.TempDir()
	dir := filepath.Join(watch.KiroSessionsDir(home), "8e890988228762f5", kiroSess)
	writeAt(t, filepath.Join(dir, "session.json"), `{"workspacePaths":["`+project+`"]}`)
	writeAt(t, filepath.Join(dir, "messages.jsonl"),
		kiroV3Line("tool_call", "a", "git status", now.Add(-10*time.Minute)), kiroV3Line("tool_result", "a", "", now.Add(-10*time.Minute+time.Second)),
		kiroV3Line("tool_call", "b", "git push --force origin main", now.Add(-5*time.Minute)), kiroV3Line("tool_result", "b", "", now.Add(-5*time.Minute+time.Second)),
	)
	ok := audit.Entry{Time: now.Add(-10 * time.Minute), Session: kiroSess, Class: "git status", Decision: "allow", Agent: "kiro"}

	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--agent=kiro"}, &stdout, &stderr, watchDeps{home: home, cwd: project, now: time.Now, entries: entriesOf(ok)})
	if code != 1 || !strings.Contains(stdout.String(), "SEM DECISÃO: git push") || !strings.Contains(stdout.String(), "2 comando(s)") || strings.Contains(stdout.String(), "--force") {
		t.Errorf("deveria acusar só o force push sem decisão (e nunca mostrar o comando):\n%d %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "não está em") || !strings.Contains(stderr.String(), "iron init --agent=kiro") {
		t.Errorf("sem o hook na pasta, deveria avisar: %q", stderr.String())
	}
}

func rolloutRow(kind, payload string, at time.Time) string {
	return `{"timestamp":"` + iso(at) + `","type":"` + kind + `","payload":` + payload + `}`
}

func TestWatchOnceCodex(t *testing.T) {
	now := time.Now()
	project := "/home/dev/proj"
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	rollout := filepath.Join(watch.CodexSessionsDir(home), "2026", "10", "01", "rollout-2026-10-01T19-59-54-"+codexSess+".jsonl")
	script := func(cmd string) string {
		return strconv.Quote("const r = await tools.exec_command({cmd:\"" + cmd + "\"});\ntext(r.output);\n")
	}
	writeAt(t, rollout,
		rolloutRow("session_meta", `{"id":"`+codexSess+`","cwd":"`+project+`"}`, now.Add(-11*time.Minute)),
		rolloutRow("response_item", `{"type":"custom_tool_call","call_id":"c1","name":"exec","input":`+script("git status")+`}`, now.Add(-10*time.Minute)),
		rolloutRow("response_item", `{"type":"custom_tool_call_output","call_id":"c1","output":[]}`, now.Add(-10*time.Minute+time.Second)),
		rolloutRow("response_item", `{"type":"custom_tool_call","call_id":"c2","name":"exec","input":`+script("git push --force origin main")+`}`, now.Add(-5*time.Minute)),
		rolloutRow("response_item", `{"type":"custom_tool_call_output","call_id":"c2","output":[]}`, now.Add(-5*time.Minute+time.Second)),
	)
	ok := audit.Entry{Time: now.Add(-10*time.Minute + 300*time.Millisecond), Session: codexSess, Class: "git status", Decision: "allow", Agent: "codex"}

	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--agent=codex"}, &stdout, &stderr, watchDeps{home: home, cwd: project, now: time.Now, entries: entriesOf(ok)})
	if code != 1 || !strings.Contains(stdout.String(), "SEM DECISÃO: git push") || !strings.Contains(stdout.String(), "2 comando(s)") {
		t.Errorf("deveria acusar só o force push sem decisão:\n%d %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "iron init --agent=codex") {
		t.Errorf("sem o hook na pasta, deveria avisar: %q", stderr.String())
	}
}

func TestWatchKiroAndCodexMissingSessionsDir(t *testing.T) {
	for agent, want := range map[string]string{"kiro": "as sessões do Kiro", "codex": "as sessões do Codex"} {
		t.Setenv("CODEX_HOME", "")
		var stdout, stderr bytes.Buffer
		code := runWatch([]string{"--once", "--agent=" + agent}, &stdout, &stderr, watchDeps{home: t.TempDir(), cwd: "/p", now: time.Now, entries: entriesOf()})
		if code != 1 || !strings.Contains(stderr.String(), want) || !strings.Contains(stderr.String(), "--dir") {
			t.Errorf("%s: %d %q", agent, code, stderr.String())
		}
	}
}
