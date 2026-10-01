package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/rules"
)

var t0 = time.Date(2026, 10, 1, 11, 16, 11, 0, time.UTC)

func ts(offset time.Duration) string { return t0.Add(offset).Format("2006-01-02T15:04:05.000Z") }

func useLine(session, id, command string, at time.Duration) string {
	return `{"type":"assistant","sessionId":"` + session + `","timestamp":"` + ts(at) + `","isSidechain":false,` +
		`"message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"` + command + `"}}]}}`
}

func resultLine(session, id string, at time.Duration) string {
	return `{"type":"user","sessionId":"` + session + `","timestamp":"` + ts(at) + `","isSidechain":false,` +
		`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok","is_error":false}]}}`
}

func TestParseTranscript(t *testing.T) {
	transcript := strings.Join([]string{
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"user","sessionId":"s1","timestamp":"` + ts(0) + `","message":{"role":"user","content":"texto simples"}}`,
		useLine("s1", "t1", "git status", time.Second),
		resultLine("s1", "t1", 2*time.Second),
		useLine("s1", "t2", "terraform plan", 3*time.Second), // sem resultado ainda
		`{"type":"assistant","sessionId":"s1","timestamp":"` + ts(4*time.Second) + `","message":{"content":[{"type":"tool_use","id":"t3","name":"Read","input":{"file_path":"/x"}}]}}`,
		`{"type":"assistant","isSidechain":true,"sessionId":"s1","timestamp":"` + ts(5*time.Second) + `","message":{"content":[{"type":"tool_use","id":"t4","name":"Bash","input":{"command":"ls"}}]}}`,
		`isto não é json`,
		``,
	}, "\n")

	calls, err := ParseTranscript(strings.NewReader(transcript))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("esperava 2 chamadas Bash (sem Read e sem subagente), obtive %+v", calls)
	}
	if calls[0].ID != "t1" || calls[0].Command != "git status" || calls[0].Session != "s1" ||
		!calls[0].At.Equal(t0.Add(time.Second)) || !calls[0].DoneAt.Equal(t0.Add(2*time.Second)) {
		t.Errorf("primeira chamada: %+v", calls[0])
	}
	if !calls[1].DoneAt.IsZero() {
		t.Errorf("a segunda não tem resultado: %+v", calls[1])
	}
}

func entry(session, class string, at time.Duration) audit.Entry {
	return audit.Entry{Time: t0.Add(at), Session: session, Class: class, Decision: "allow", Agent: "claude"}
}

func source(entries ...audit.Entry) Source {
	return Source{
		Classify: rules.Classify,
		Entries: func(since time.Time) ([]audit.Entry, error) {
			var out []audit.Entry
			for _, e := range entries {
				if !e.Time.Before(since) {
					out = append(out, e)
				}
			}
			return out, nil
		},
	}
}

func call(id, command string, at, done time.Duration) event {
	c := Call{Session: "s1", ID: id, Command: command, At: t0.Add(at)}
	ev := event{calls: []Call{c}, results: map[string]time.Time{}}
	if done > 0 {
		ev.results[id] = t0.Add(done)
	}
	return ev
}

func TestTrackerCoverage(t *testing.T) {
	cases := []struct {
		name    string
		entries []audit.Entry
		wantGap bool
	}{
		{"com decisão no intervalo", []audit.Entry{entry("s1", "git push", 2*time.Second)}, false},
		{"sem nenhuma decisão", nil, true},
		{"decisão de outra sessão", []audit.Entry{entry("s2", "git push", 2*time.Second)}, true},
		{"decisão sem sessão vale para qualquer uma", []audit.Entry{entry("", "git push", 2*time.Second)}, false},
		{"decisão de outra classe", []audit.Entry{entry("s1", "git status", 2*time.Second)}, true},
		{"decisão muito antes da chamada", []audit.Entry{entry("s1", "git push", -time.Minute)}, true},
		{"decisão muito depois do resultado", []audit.Entry{entry("s1", "git push", 2*time.Minute)}, true},
		{"folga de relógio antes", []audit.Entry{entry("s1", "git push", time.Second-time.Second/2)}, false},
		{"o próprio aviso do watch não conta", []audit.Entry{{Time: t0.Add(2 * time.Second), Session: "s1", Class: "git push", Decision: "gap", Rule: "watch"}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := NewTracker(source(c.entries...), DefaultGrace)
			tr.Add(call("a", "git push origin main", time.Second, 3*time.Second))

			gaps, err := tr.Check(t0.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if (len(gaps) == 1) != c.wantGap {
				t.Errorf("lacunas: %+v (esperava lacuna=%v)", gaps, c.wantGap)
			}
			if c.wantGap && gaps[0].Class != "git push" {
				t.Errorf("classe: %q", gaps[0].Class)
			}
			if tr.Checked != 1 {
				t.Errorf("Checked = %d", tr.Checked)
			}
		})
	}
}

func TestTrackerGraceAndPending(t *testing.T) {
	tr := NewTracker(source(), 3*time.Second)
	tr.Add(call("a", "git push", time.Second, 2*time.Second))
	tr.Add(call("b", "git push", 3*time.Second, 0)) // sem resultado

	if gaps, _ := tr.Check(t0.Add(3 * time.Second)); len(gaps) != 0 {
		t.Errorf("antes do prazo de graça não acusa: %+v", gaps)
	}
	gaps, _ := tr.Check(t0.Add(6 * time.Second))
	if len(gaps) != 1 || gaps[0].Call.ID != "a" {
		t.Errorf("só a chamada com resultado vencido: %+v", gaps)
	}
	if gaps, _ := tr.Check(t0.Add(10 * time.Second)); len(gaps) != 0 {
		t.Errorf("cada chamada é conferida uma vez: %+v", gaps)
	}

	// O resultado da outra chega depois.
	tr.Add(event{results: map[string]time.Time{"b": t0.Add(8 * time.Second)}})
	if gaps, _ := tr.Check(t0.Add(20 * time.Second)); len(gaps) != 1 || gaps[0].Call.ID != "b" {
		t.Errorf("a segunda deve ser acusada quando o resultado volta: %+v", gaps)
	}
}

func TestTrackerTwoIdenticalCommandsNeedTwoDecisions(t *testing.T) {
	tr := NewTracker(source(entry("s1", "git push", 2*time.Second)), DefaultGrace)
	tr.Add(call("a", "git push", time.Second, 3*time.Second))
	tr.Add(call("b", "git push", time.Second, 3*time.Second))

	gaps, _ := tr.Check(t0.Add(time.Minute))
	if len(gaps) != 1 || tr.Covered != 1 {
		t.Errorf("uma decisão cobre uma chamada só: lacunas=%d cobertas=%d", len(gaps), tr.Covered)
	}
}

func TestTrackerForgetsCallsWithoutResult(t *testing.T) {
	tr := NewTracker(source(), 0)
	tr.Add(call("a", "git push", 0, 0))
	tr.Check(t0.Add(2 * time.Hour))
	if len(tr.pending) != 0 || len(tr.order) != 0 {
		t.Errorf("chamada sem resultado há horas deve ser esquecida: %+v", tr.pending)
	}
}

func TestTailReadsOnlyNewCompleteLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(s string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		f.WriteString(s)
	}

	write("antiga\n")
	tl := &tail{path: path}
	tl.atEnd()

	write("um\ndois\ntre")
	got, err := tl.lines()
	if err != nil || len(got) != 2 || string(got[0]) != "um" || string(got[1]) != "dois" {
		t.Fatalf("linhas: %q %v", got, err)
	}

	write("s\nquatro\n")
	got, _ = tl.lines()
	if len(got) != 2 || string(got[0]) != "tres" || string(got[1]) != "quatro" {
		t.Errorf("a linha parcial deve ser completada: %q", got)
	}

	if got, _ = tl.lines(); len(got) != 0 {
		t.Errorf("sem novidade: %q", got)
	}

	// Arquivo recriado menor que o deslocamento: lê do começo.
	os.WriteFile(path, []byte("novo\n"), 0o600)
	if got, _ = tl.lines(); len(got) != 1 || string(got[0]) != "novo" {
		t.Errorf("arquivo recriado: %q", got)
	}
}

func TestWatcherEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	os.WriteFile(path, []byte(useLine("s1", "velha", "git push", 0)+"\n"), 0o600) // já existia: não conta

	w := New([]string{dir}, source(entry("s1", "git status", 11*time.Second)), time.Second, t0)

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(useLine("s1", "ok", "git status", 10*time.Second) + "\n")
	f.WriteString(resultLine("s1", "ok", 12*time.Second) + "\n")
	f.WriteString(useLine("s1", "falha", "git push --force", 20*time.Second) + "\n")
	f.WriteString(resultLine("s1", "falha", 22*time.Second) + "\n")
	f.Close()

	gaps, err := w.Poll(t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].Call.ID != "falha" || gaps[0].Class != "git push" {
		t.Fatalf("esperava só a lacuna do git push: %+v", gaps)
	}
	if checked, covered := w.Stats(); checked != 2 || covered != 1 {
		t.Errorf("stats: %d conferidas, %d cobertas", checked, covered)
	}

	// Sessão nova criada depois: lida desde o começo.
	os.WriteFile(filepath.Join(dir, "s2.jsonl"), []byte(useLine("s2", "n", "terraform destroy", 30*time.Second)+"\n"+resultLine("s2", "n", 31*time.Second)+"\n"), 0o600)
	gaps, _ = w.Poll(t0.Add(2 * time.Minute))
	if len(gaps) != 1 || gaps[0].Class != "terraform destroy" {
		t.Errorf("sessão nova: %+v", gaps)
	}
}

func TestScanReport(t *testing.T) {
	dir := t.TempDir()
	lines := strings.Join([]string{
		useLine("s1", "a", "git status", 10*time.Second), resultLine("s1", "a", 11*time.Second),
		useLine("s1", "b", "git push --force", 20*time.Second), resultLine("s1", "b", 21*time.Second),
		useLine("s1", "c", "ls", -time.Hour), resultLine("s1", "c", -time.Hour+time.Second), // antes do período
	}, "\n")
	os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(lines+"\n"), 0o600)

	gaps, checked, covered, err := Scan([]string{dir}, source(entry("s1", "git status", 10*time.Second)), t0)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || covered != 1 || len(gaps) != 1 || gaps[0].Call.ID != "b" {
		t.Errorf("checked=%d covered=%d gaps=%+v", checked, covered, gaps)
	}
}

func TestProjectDirName(t *testing.T) {
	if got := ProjectDirName("/Users/user/Documents/IronDeploy"); got != "-Users-user-Documents-IronDeploy" {
		t.Errorf("obtive %q", got)
	}
	if got := ProjectDirName("/home/a.b/meu_proj"); got != "-home-a-b-meu-proj" {
		t.Errorf("obtive %q", got)
	}
	if got := TranscriptDir("/h", "/p/x"); got != filepath.Join("/h", ".claude", "projects", "-p-x") {
		t.Errorf("obtive %q", got)
	}
}
