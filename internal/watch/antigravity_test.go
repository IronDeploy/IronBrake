package watch

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/rules"
)

const agySession = "22d4da59-96e7-4ba2-b985-74477ca3618e"

func agyTime(offset time.Duration) string { return t0.Add(offset).Format("2006-01-02T15:04:05Z") }

// agyPlanner monta um passo PLANNER_RESPONSE; cada chamada é "ferramenta|comando|cwd".
func agyPlanner(step int, at time.Duration, calls ...string) string {
	var tcs []string
	for _, c := range calls {
		parts := strings.SplitN(c, "|", 3)
		tcs = append(tcs, `{"name":"`+parts[0]+`","args":{"CommandLine":"`+parts[1]+`","Cwd":"`+parts[2]+`","WaitMsBeforeAsync":5000,"toolAction":"x"}}`)
	}
	return `{"step_index":` + strconv.Itoa(step) + `,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"` + agyTime(at) + `","tool_calls":[` + strings.Join(tcs, ",") + `]}`
}

func agyResult(step int, at time.Duration, status string) string {
	return `{"step_index":` + strconv.Itoa(step) + `,"source":"MODEL","type":"GENERIC","status":"` + status + `","created_at":"` + agyTime(at) + `","content":"ok"}`
}

func writeAgyTranscript(t *testing.T, brain, session string, lines ...string) string {
	t.Helper()
	path := filepath.Join(brain, session, ".system_generated", "logs", "transcript_full.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgyParserRealShapes(t *testing.T) {
	path := "/h/.gemini/antigravity-cli/brain/" + agySession + "/.system_generated/logs/transcript_full.jsonl"
	parse := agyParser(path)

	// passos reais: pedido do usuário, leitura de arquivo, chamada de shell, resultado com erro do hook.
	if _, ok := parse([]byte(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"` + agyTime(0) + `"}`)); ok {
		t.Error("USER_INPUT não é chamada nem resultado de chamada")
	}
	if ev, ok := parse([]byte(agyPlanner(1, time.Second, "view_file|x|/p"))); ok && len(ev.calls) != 0 {
		t.Errorf("view_file não é shell: %+v", ev)
	}

	ev, ok := parse([]byte(agyPlanner(3, 6*time.Second, "run_command|git push --force-with-lease origin main|/p")))
	if !ok || len(ev.calls) != 1 {
		t.Fatalf("deveria ler a chamada de shell: %+v", ev)
	}
	c := ev.calls[0]
	if c.Session != agySession || c.Command != "git push --force-with-lease origin main" || c.Cwd != "/p" ||
		!c.At.Equal(t0.Add(6*time.Second)) || c.ID != agySession+"#3#0" {
		t.Errorf("chamada inesperada: %+v", c)
	}

	// o passo de resultado (GENERIC) começa antes de o hook terminar: não fecha a chamada.
	if ev, ok := parse([]byte(agyResult(4, 8*time.Second, "ERROR"))); ok {
		t.Errorf("GENERIC não deveria fechar a chamada: %+v", ev)
	}
	// a resposta seguinte do modelo (passo 5) fecha a chamada do passo 3.
	ev, ok = parse([]byte(agyPlanner(5, 11*time.Second)))
	if !ok || !ev.results[agySession+"#3#0"].Equal(t0.Add(11*time.Second)) {
		t.Errorf("o passo n+2 fecha a chamada do passo n: %+v", ev)
	}

	for _, junk := range []string{"", "isto não é json", "{"} {
		if _, ok := parse([]byte(junk)); ok {
			t.Errorf("%q não deveria ser lido", junk)
		}
	}
}

func TestInProject(t *testing.T) {
	keep := InProject("/home/dev/projeto/")
	cases := map[string]bool{
		"/home/dev/projeto": true, "/home/dev/projeto/sub/x": true, "/home/dev/projeto/../projeto": true,
		"/home/dev/projeto2": false, "/home/dev": false, "/tmp": false, "": false,
	}
	for cwd, want := range cases {
		if got := keep(Call{Cwd: cwd}); got != want {
			t.Errorf("InProject(%q) = %v, quero %v", cwd, got, want)
		}
	}
}

func agySource(project string, entries ...audit.Entry) Source {
	src := source(entries...)
	src.Format = &AntigravityFormat
	src.Keep = InProject(project)
	src.Classify = rules.Classify
	return src
}

func TestScanAntigravity(t *testing.T) {
	brain := t.TempDir()
	writeAgyTranscript(t, brain, agySession,
		agyResult(0, 0, "DONE"),
		agyPlanner(1, time.Second, "run_command|git status|/p/sub"), // com decisão
		agyResult(2, 3*time.Second, "DONE"),
		agyPlanner(3, 5*time.Second, "run_command|git push --force origin main|/p"), // SEM decisão
		agyResult(4, 7*time.Second, "ERROR"),
		agyPlanner(5, 9*time.Second, "run_command|git status|/outro-projeto"), // de outro projeto: fora
		agyResult(6, 11*time.Second, "DONE"),
		agyPlanner(7, 12*time.Second, "run_command|git status|/p"), // sem o passo n+2 ainda: não terminou
		agyResult(8, 13*time.Second, "DONE"),
	)

	gaps, checked, covered, err := Scan([]string{brain}, agySource("/p", entry(agySession, "git status", 2*time.Second)), t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || covered != 1 || len(gaps) != 1 || gaps[0].Class != "git push" || gaps[0].Call.Session != agySession {
		t.Errorf("conferidas %d, com decisão %d, lacunas %+v", checked, covered, gaps)
	}
}

// Regressão (horários reais, 2026-10-01): o hook esperou o clique na janela e
// gravou a decisão DEPOIS do início do passo de resultado, mas antes da
// resposta seguinte do modelo. Não pode virar lacuna.
func TestScanAntigravityDecisionAfterResultStepStarted(t *testing.T) {
	brain := t.TempDir()
	writeAgyTranscript(t, brain, agySession,
		agyPlanner(3, 0, "run_command|git push --force-with-lease origin main|/p"),
		agyResult(4, 2*time.Second, "DONE"), // o passo começa aqui...
		agyPlanner(5, 5*time.Second),        // ...e o modelo só responde aqui
	)
	late := audit.Entry{Time: t0.Add(4*time.Second + 700*time.Millisecond), Session: agySession, Class: "git push", Decision: "userApproved", Agent: "antigravity"}

	gaps, checked, covered, err := Scan([]string{brain}, agySource("/p", late), t0.Add(-time.Hour))
	if err != nil || checked != 1 || covered != 1 || len(gaps) != 0 {
		t.Errorf("a decisão gravada antes da resposta seguinte cobre a chamada: %d %d %+v %v", checked, covered, gaps, err)
	}
}

func TestScanAntigravityHookBlockedCallStillNeedsItsDecision(t *testing.T) {
	brain := t.TempDir()
	writeAgyTranscript(t, brain, agySession,
		agyPlanner(1, 0, "run_command|git push --force origin main|/p"),
		agyResult(2, 2*time.Second, "ERROR"), // negado pelo hook
		agyPlanner(3, 3*time.Second),
	)
	deny := audit.Entry{Time: t0.Add(time.Second), Session: agySession, Class: "git push", Decision: "deny", Agent: "antigravity"}
	_, checked, covered, err := Scan([]string{brain}, agySource("/p", deny), t0.Add(-time.Hour))
	if err != nil || checked != 1 || covered != 1 {
		t.Errorf("a decisão deny cobre a chamada: %d %d %v", checked, covered, err)
	}
}

func TestWatcherAntigravityFollowsNewConversations(t *testing.T) {
	brain := t.TempDir()
	w := New([]string{brain}, agySource("/p"), 0, t0)

	if gaps, err := w.Poll(t0); err != nil || len(gaps) != 0 {
		t.Fatalf("sem conversas: %+v %v", gaps, err)
	}
	// conversa criada depois do início: lida desde o começo.
	writeAgyTranscript(t, brain, agySession,
		agyPlanner(1, time.Second, "run_command|terraform plan|/p"),
		agyResult(2, 3*time.Second, "DONE"),
		agyPlanner(3, 4*time.Second),
	)
	gaps, err := w.Poll(t0.Add(time.Minute))
	if err != nil || len(gaps) != 1 || gaps[0].Class != "terraform plan" {
		t.Errorf("deveria acusar a chamada sem decisão: %+v %v", gaps, err)
	}
	if checked, covered := w.Stats(); checked != 1 || covered != 0 {
		t.Errorf("estatísticas: %d %d", checked, covered)
	}
}

func TestAgyFilesIgnoresOtherFiles(t *testing.T) {
	brain := t.TempDir()
	good := writeAgyTranscript(t, brain, agySession, agyResult(0, 0, "DONE"))
	other := filepath.Join(brain, agySession, ".system_generated", "logs", "transcript.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := agyFiles(brain); len(files) != 1 || files[0] != good {
		t.Errorf("só o transcript_full.jsonl conta: %v", files)
	}
}
