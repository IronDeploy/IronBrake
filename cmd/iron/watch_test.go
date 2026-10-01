package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/watch"
)

func TestParseWatchArgs(t *testing.T) {
	got, err := parseWatchArgs([]string{"--once", "--since", "30m", "--dir=/x", "--notify", "--grace", "5s", "--all"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.once || got.since != 30*time.Minute || got.dir != "/x" || !got.notify || got.grace != 5*time.Second || !got.all {
		t.Errorf("opções: %+v", got)
	}

	def, _ := parseWatchArgs(nil)
	if def.once || def.since != time.Hour || def.grace != watch.DefaultGrace {
		t.Errorf("padrões: %+v", def)
	}

	for _, bad := range [][]string{{"--since"}, {"--since", "abc"}, {"--since", "-5m"}, {"--dir"}, {"--nada"}} {
		if _, err := parseWatchArgs(bad); err == nil {
			t.Errorf("%v deveria dar erro", bad)
		}
	}
}

const (
	wCwd     = "/work/projeto"
	wSession = "11111111-2222-3333-4444-555555555555"
)

func wLine(kind, id, command string, at time.Time) string {
	stamp := at.UTC().Format("2006-01-02T15:04:05.000Z")
	if kind == "use" {
		return `{"type":"assistant","sessionId":"` + wSession + `","timestamp":"` + stamp + `","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"` + command + `"}}]}}`
	}
	return `{"type":"user","sessionId":"` + wSession + `","timestamp":"` + stamp + `","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}}`
}

// watchHome monta um HOME com o transcript da pasta wCwd.
func watchHome(t *testing.T, lines ...string) (home, dir string) {
	t.Helper()
	home = t.TempDir()
	dir = watch.TranscriptDir(home, wCwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, wSession+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, dir
}

func entriesOf(entries ...audit.Entry) func(time.Time) ([]audit.Entry, error) {
	return func(since time.Time) ([]audit.Entry, error) {
		var out []audit.Entry
		for _, e := range entries {
			if !e.Time.Before(since) {
				out = append(out, e)
			}
		}
		return out, nil
	}
}

func TestWatchOnceReportsGapsAndExitCode(t *testing.T) {
	now := time.Now()
	home, _ := watchHome(t,
		wLine("use", "a", "git status", now.Add(-10*time.Minute)), wLine("result", "a", "", now.Add(-10*time.Minute+time.Second)),
		wLine("use", "b", "git push --force", now.Add(-5*time.Minute)), wLine("result", "b", "", now.Add(-5*time.Minute+time.Second)),
	)
	ok := audit.Entry{Time: now.Add(-10*time.Minute + 500*time.Millisecond), Session: wSession, Class: "git status", Decision: "allow"}

	cases := []struct {
		name     string
		entries  []audit.Entry
		wantCode int
		want     []string
	}{
		{"uma lacuna", []audit.Entry{ok}, 1, []string{"SEM DECISÃO: git push", "2 comando(s)", "1 SEM decisão"}},
		{"nada coberto", nil, 1, []string{"nenhum foi visto pelo Iron Brake"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWatch([]string{"--once"}, &stdout, &stderr, watchDeps{
				home: home, cwd: wCwd, now: time.Now, entries: entriesOf(c.entries...),
			})
			if code != c.wantCode {
				t.Errorf("código: esperava %d, obtive %d\n%s", c.wantCode, code, stdout.String())
			}
			for _, w := range c.want {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("saída deveria conter %q:\n%s", w, stdout.String())
				}
			}
			if strings.Contains(stdout.String(), "--force") {
				t.Errorf("a saída nunca mostra o comando: %s", stdout.String())
			}
			if !strings.Contains(stderr.String(), "não está instalado") {
				t.Errorf("sem hook em %s, deveria avisar: %q", wCwd, stderr.String())
			}
		})
	}
}

func TestWatchOnceCapsTheList(t *testing.T) {
	now := time.Now()
	var lines []string
	for i := 0; i < 20; i++ {
		id := "c" + strconv.Itoa(i)
		at := now.Add(-time.Duration(30-i) * time.Minute)
		lines = append(lines, wLine("use", id, "git push --force", at), wLine("result", id, "", at.Add(time.Second)))
	}
	home, _ := watchHome(t, lines...)

	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once"}, &stdout, &stderr, watchDeps{home: home, cwd: wCwd, now: time.Now, entries: entriesOf()})

	if code != 1 || strings.Count(stdout.String(), "SEM DECISÃO:") != maxListedGaps || !strings.Contains(stdout.String(), "e mais 5") {
		t.Errorf("a lista deve parar em %d: %d %s", maxListedGaps, code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "20 comando(s)") || !strings.Contains(stdout.String(), "20 SEM decisão") {
		t.Errorf("o resumo conta todas: %s", stdout.String())
	}
}

func TestWarnIfHookMissing(t *testing.T) {
	iron := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/x/iron","args":["hook"]}]}]}}`
	write := func(dir, content string) {
		path := setup.SettingsPath(dir)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(content), 0o644)
	}

	cases := []struct {
		name        string
		project     string
		user        string
		wantWarning bool
	}{
		{"sem nenhum", "", "", true},
		{"no projeto", iron, "", false},
		{"no usuário", "", iron, false},
		{"arquivo ilegível não afirma nada", "{", "", false},
		{"settings sem o hook", `{"model":"x"}`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			project, home := t.TempDir(), t.TempDir()
			if c.project != "" {
				write(project, c.project)
			}
			if c.user != "" {
				write(home, c.user)
			}
			var stderr bytes.Buffer
			warnIfHookMissing(project, home, &stderr)
			if got := stderr.Len() > 0; got != c.wantWarning {
				t.Errorf("aviso=%v, esperava %v (%q)", got, c.wantWarning, stderr.String())
			}
		})
	}
}

func TestWatchOnceAllCovered(t *testing.T) {
	now := time.Now()
	home, _ := watchHome(t, wLine("use", "a", "git status", now.Add(-time.Minute)), wLine("result", "a", "", now.Add(-time.Minute+time.Second)))
	e := audit.Entry{Time: now.Add(-time.Minute + 500*time.Millisecond), Session: wSession, Class: "git status", Decision: "allow"}

	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--since", "1h"}, &stdout, &stderr, watchDeps{home: home, cwd: wCwd, now: time.Now, entries: entriesOf(e)})

	if code != 0 || !strings.Contains(stdout.String(), "todos com decisão") {
		t.Errorf("esperava sucesso: %d %s", code, stdout.String())
	}
}

func TestWatchOnceNoCommands(t *testing.T) {
	home, _ := watchHome(t, `{"type":"queue-operation"}`)
	var stdout, stderr bytes.Buffer

	code := runWatch([]string{"--once"}, &stdout, &stderr, watchDeps{home: home, cwd: wCwd, now: time.Now, entries: entriesOf()})

	if code != 0 || !strings.Contains(stdout.String(), "nenhum comando") {
		t.Errorf("%d %s", code, stdout.String())
	}
}

func TestWatchMissingTranscriptDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once"}, &stdout, &stderr, watchDeps{home: t.TempDir(), cwd: wCwd, now: time.Now, entries: entriesOf()})

	if code != 1 || !strings.Contains(stderr.String(), "--dir ou --all") {
		t.Errorf("%d %q", code, stderr.String())
	}
}

func TestWatchUsageErrorExits2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWatch([]string{"--nada"}, &stdout, &stderr, watchDeps{}); code != 2 {
		t.Errorf("esperava 2, obtive %d", code)
	}
}

func TestWatchAllScansEveryProject(t *testing.T) {
	now := time.Now()
	home, _ := watchHome(t, wLine("use", "a", "git push --force", now.Add(-time.Minute)), wLine("result", "a", "", now.Add(-time.Minute+time.Second)))
	other := filepath.Join(home, ".claude", "projects", "-outro")
	os.MkdirAll(other, 0o755)

	var stdout, stderr bytes.Buffer
	code := runWatch([]string{"--once", "--all"}, &stdout, &stderr, watchDeps{home: home, cwd: "/qualquer", now: time.Now, entries: entriesOf()})

	if code != 1 || !strings.Contains(stdout.String(), "SEM DECISÃO: git push") {
		t.Errorf("%d %s", code, stdout.String())
	}
	if strings.Contains(stderr.String(), "não está instalado") {
		t.Errorf("com --all não confere o hook de uma pasta só: %q", stderr.String())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestWatchFollowRecordsGapAndNotifies(t *testing.T) {
	home, dir := watchHome(t, `{"type":"queue-operation"}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var recorded []audit.Entry
	var notified []string
	stdout := &syncBuffer{}
	var stderr bytes.Buffer
	done := make(chan int, 1)

	go func() {
		done <- runWatch([]string{"--notify", "--grace", "1ms", "--dir", dir}, stdout, &stderr, watchDeps{
			ctx: ctx, home: home, cwd: wCwd, now: time.Now, poll: 20 * time.Millisecond,
			entries: entriesOf(),
			record:  func(e audit.Entry) error { mu.Lock(); recorded = append(recorded, e); mu.Unlock(); return nil },
			notify:  func(title, message string) { mu.Lock(); notified = append(notified, message); mu.Unlock() },
		})
	}()

	time.Sleep(100 * time.Millisecond)
	now := time.Now()
	f, _ := os.OpenFile(filepath.Join(dir, wSession+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(wLine("use", "x", "terraform destroy", now) + "\n" + wLine("result", "x", "", now.Add(10*time.Millisecond)) + "\n")
	f.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(recorded)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("código: %d", code)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 1 {
		t.Fatalf("esperava 1 aviso gravado, obtive %+v\n%s", recorded, stdout.String())
	}
	got := recorded[0]
	if got.Decision != "gap" || got.Rule != "watch" || got.Class != "terraform destroy" || got.Session != wSession || got.Agent != "claude" {
		t.Errorf("entrada: %+v", got)
	}
	if len(notified) != 1 || strings.Contains(notified[0], "destroy ") && strings.Contains(notified[0], "-force") {
		t.Errorf("notificação: %v", notified)
	}
	if !strings.Contains(stdout.String(), "SEM DECISÃO: terraform destroy") || !strings.Contains(stdout.String(), "1 sem") {
		t.Errorf("saída: %s", stdout.String())
	}
}
