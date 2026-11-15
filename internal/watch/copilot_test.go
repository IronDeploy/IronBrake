package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const copilotSession = "05641926-7973-460d-935d-0a7a90c69f2a"

func copilotStart(id, tool, command string, at time.Duration) string {
	return `{"type":"tool.execution_start","data":{"toolCallId":"` + id + `","toolName":"` + tool +
		`","arguments":{"command":"` + command + `","description":"d"}},"id":"x","timestamp":"` + isoMs(at) + `"}`
}

func copilotDone(id string, at time.Duration) string {
	return `{"type":"tool.execution_complete","data":{"toolCallId":"` + id + `","success":true},"id":"y","timestamp":"` + isoMs(at) + `"}`
}

// writeCopilotSession grava a pasta da sessão com o workspace.yaml, como o Copilot.
func writeCopilotSession(t *testing.T, root, cwdLine string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, copilotSession)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "id: " + copilotSession + "\n" + cwdLine + "\nbranch: main\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events.jsonl")
	writeLines(t, path, lines...)
	return path
}

// Os formatos abaixo são os do events.jsonl real do Copilot 1.0.112.
func TestCopilotParserRealShapes(t *testing.T) {
	path := writeCopilotSession(t, t.TempDir(), "cwd: /work/proj")
	parse := copilotParser(path)

	ev, ok := parse([]byte(copilotStart("call_1", "bash", "git status --short", time.Second)))
	if !ok || len(ev.calls) != 1 {
		t.Fatalf("deveria ler a chamada: %+v", ev)
	}
	c := ev.calls[0]
	if c.Session != copilotSession || c.Command != "git status --short" || c.Cwd != "/work/proj" || c.ID != "call_1" || !c.At.Equal(t0.Add(time.Second)) {
		t.Errorf("chamada inesperada: %+v", c)
	}
	if ps, _ := parse([]byte(copilotStart("call_2", "powershell", "Get-Date", 2*time.Second))); len(ps.calls) != 1 {
		t.Errorf("powershell também é shell: %+v", ps)
	}

	ev, ok = parse([]byte(copilotDone("call_1", 3*time.Second)))
	if !ok || !ev.results["call_1"].Equal(t0.Add(3*time.Second)) {
		t.Errorf("o resultado deveria fechar a chamada: %+v", ev)
	}

	for name, line := range map[string]string{
		"outra ferramenta": copilotStart("c", "view", "x", 0),
		"sem comando":      `{"type":"tool.execution_start","data":{"toolCallId":"c","toolName":"bash","arguments":{}},"timestamp":"` + isoMs(0) + `"}`,
		"hook.start":       `{"type":"hook.start","data":{"hookType":"preToolUse"},"timestamp":"` + isoMs(0) + `"}`,
		"mensagem":         `{"type":"assistant.message","data":{},"timestamp":"` + isoMs(0) + `"}`,
		"lixo":             "isto não é json",
	} {
		if ev, ok := parse([]byte(line)); ok {
			t.Errorf("%s não deveria ser lido: %+v", name, ev)
		}
	}
}

func TestCopilotWorkspaceCwd(t *testing.T) {
	for name, c := range map[string]struct{ line, want string }{
		"simples":         {"cwd: /work/proj", "/work/proj"},
		"com espaço":      {"cwd: /work/meu projeto", "/work/meu projeto"},
		"aspas simples":   {"cwd: '/work/o''brien'", "/work/o'brien"},
		"aspas duplas":    {`cwd: "/work/a\\b"`, `/work/a\b`},
		"sem a chave":     {"branch: main", ""},
		"aspas inválidas": {`cwd: "/work/\q"`, ""},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte("id: x\n"+c.line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := copilotWorkspaceCwd(dir); got != c.want {
			t.Errorf("%s: %q; esperava %q", name, got, c.want)
		}
	}
	if got := copilotWorkspaceCwd(t.TempDir()); got != "" {
		t.Errorf("sem workspace.yaml: %q", got)
	}
}

func TestScanCopilot(t *testing.T) {
	root := t.TempDir()
	writeCopilotSession(t, root, "cwd: /work/proj",
		copilotStart("c1", "bash", "git status", time.Second), copilotDone("c1", 2*time.Second),
		copilotStart("c2", "bash", "git push --force origin main", 5*time.Second), copilotDone("c2", 6*time.Second), // SEM decisão
		copilotStart("c3", "bash", "git status", 11*time.Second), // sem resultado ainda
	)
	gaps, checked, covered, err := Scan([]string{root}, fmtSource(&CopilotFormat, "/work/proj", entry(copilotSession, "git status", time.Second+300*time.Millisecond)), t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || covered != 1 || len(gaps) != 1 || gaps[0].Class != "git push" || gaps[0].Call.Session != copilotSession {
		t.Errorf("conferidas %d, com decisão %d, lacunas %+v", checked, covered, gaps)
	}
	// outro projeto fica de fora.
	if _, checked, _, _ = Scan([]string{root}, fmtSource(&CopilotFormat, "/outro", entry(copilotSession, "git status", 0)), t0.Add(-time.Hour)); checked != 0 {
		t.Errorf("de outro projeto não conta: %d", checked)
	}
}

func TestCopilotFilesAndSessionsDir(t *testing.T) {
	root := t.TempDir()
	good := writeCopilotSession(t, root, "cwd: /work/proj")
	writeLines(t, filepath.Join(root, "outra", "notas.jsonl"), "{}")
	if files := copilotFiles(root); len(files) != 1 || files[0] != good {
		t.Errorf("só <sessão>/events.jsonl conta: %v", files)
	}

	t.Setenv("COPILOT_HOME", "")
	if got := CopilotSessionsDir("/home/u"); got != filepath.Join("/home/u", ".copilot", "session-state") {
		t.Errorf("padrão: %q", got)
	}
	t.Setenv("COPILOT_HOME", "/x/ch")
	if got := CopilotSessionsDir("/home/u"); got != filepath.Join("/x/ch", "session-state") {
		t.Errorf("COPILOT_HOME: %q", got)
	}
}

func copilotDenied(id string, at time.Duration) string {
	return `{"type":"tool.execution_complete","data":{"toolCallId":"` + id + `","success":false,"error":{"message":"Denied by preToolUse hook: x","code":"denied"}},"id":"z","timestamp":"` + isoMs(at) + `"}`
}

// Um comando que o Copilot recusou não rodou: não há lacuna a acusar, mesmo sem
// decisão no log (o Copilot para no primeiro deny e pode nem consultar o Iron Brake).
func TestScanCopilotIgnoresBlockedCalls(t *testing.T) {
	root := t.TempDir()
	writeCopilotSession(t, root, "cwd: /work/proj",
		copilotStart("c1", "bash", "git push --force origin main", time.Second), copilotDenied("c1", 2*time.Second), // negado por outro hook, sem decisão
		copilotStart("c2", "bash", "git push --force origin main", 5*time.Second), copilotDone("c2", 6*time.Second), // rodou, sem decisão
	)
	gaps, checked, _, err := Scan([]string{root}, fmtSource(&CopilotFormat, "/work/proj"), t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if checked != 1 || len(gaps) != 1 || gaps[0].Call.ID != "c2" {
		t.Errorf("só o comando que rodou conta: conferidas %d, lacunas %+v", checked, gaps)
	}
}

func TestTrackerDropsBlockedCalls(t *testing.T) {
	tr := NewTracker(fmtSource(&CopilotFormat, "/work/proj"), 0)
	c := Call{Session: "s", ID: "c1", Command: "git push --force origin main", Cwd: "/work/proj", At: t0}
	tr.Add(event{calls: []Call{c}, results: map[string]time.Time{}})
	tr.Add(event{results: map[string]time.Time{"c1": t0.Add(time.Second)}, blocked: []string{"c1"}})
	if gaps, err := tr.Check(t0.Add(time.Hour)); err != nil || len(gaps) != 0 || tr.Checked != 0 {
		t.Errorf("chamada bloqueada não é conferida: %+v %v checked=%d", gaps, err, tr.Checked)
	}
}
