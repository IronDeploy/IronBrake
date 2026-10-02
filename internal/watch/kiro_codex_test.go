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

func isoMs(offset time.Duration) string { return t0.Add(offset).Format("2006-01-02T15:04:05.000Z") }

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fmtSource(format *Format, project string, entries ...audit.Entry) Source {
	src := source(entries...)
	src.Format, src.Keep, src.Classify = format, InProject(project), rules.Classify
	return src
}

// ---- Kiro v3

const kiroV3Session = "sess_379f8601-8fb7-45f9-b0d1-20257a458405"

func v3Call(id, command, cwd, status string, at time.Duration) string {
	return `{"id":"x","timestamp":"` + isoMs(at) + `","payload":{"type":"tool_call","toolCallId":"` + id + `","toolName":"execute_bash","args":{"command":"` + command +
		`","description":"d","cwd":` + cwd + `,"run_in_background":false,"timeout":null},"status":"` + status + `","kind":"execute"}}`
}

func v3Result(id string, at time.Duration) string {
	return `{"id":"y","timestamp":"` + isoMs(at) + `","payload":{"type":"tool_result","toolCallId":"` + id + `","content":"{}","success":true}}`
}

func writeV3(t *testing.T, root string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, "8e890988228762f5", kiroV3Session)
	writeLines(t, filepath.Join(dir, "session.json"), `{"schemaVersion":"1.0.0","id":"`+kiroV3Session+`","workspacePaths":["/work/proj"],"rootPaths":["/work/proj"]}`)
	path := filepath.Join(dir, "messages.jsonl")
	writeLines(t, path, lines...)
	return path
}

func TestKiroV3Parser(t *testing.T) {
	root := t.TempDir()
	path := writeV3(t, root)
	parse := kiroParser(path)

	if _, ok := parse([]byte(`{"timestamp":"` + isoMs(0) + `","payload":{"type":"turn_start"}}`)); ok {
		t.Error("turn_start não é chamada")
	}
	// cwd vazio: vale o workspacePaths do session.json.
	ev, ok := parse([]byte(v3Call("tooluse_a", "git status", "null", "completed", time.Second)))
	if !ok || len(ev.calls) != 1 {
		t.Fatalf("deveria ler a chamada: %+v", ev)
	}
	c := ev.calls[0]
	if c.Session != kiroV3Session || c.Command != "git status" || c.Cwd != "/work/proj" || c.ID != "tooluse_a" || !c.At.Equal(t0.Add(time.Second)) {
		t.Errorf("chamada inesperada: %+v", c)
	}
	// cwd da chamada vence o do workspace.
	if ev, _ := parse([]byte(v3Call("tooluse_b", "ls", `"/work/proj/sub"`, "completed", 0))); ev.calls[0].Cwd != "/work/proj/sub" {
		t.Errorf("o cwd da chamada deveria valer: %+v", ev.calls[0])
	}
	// denied não executou.
	if ev, ok := parse([]byte(v3Call("tooluse_c", "git push --force origin main", "null", "denied", 0))); ok && len(ev.calls) != 0 {
		t.Errorf("chamada negada não conta: %+v", ev)
	}
	// outra ferramenta não é shell.
	other := `{"timestamp":"` + isoMs(0) + `","payload":{"type":"tool_call","toolCallId":"z","toolName":"read_file","args":{"path":"x"},"status":"completed"}}`
	if ev, ok := parse([]byte(other)); ok && len(ev.calls) != 0 {
		t.Errorf("read_file não é shell: %+v", ev)
	}
	if ev, _ := parse([]byte(v3Result("tooluse_a", 2*time.Second))); !ev.results["tooluse_a"].Equal(t0.Add(2 * time.Second)) {
		t.Errorf("o resultado fecha a chamada: %+v", ev)
	}
	for _, junk := range []string{"", "isto não é json", "{"} {
		if _, ok := parse([]byte(junk)); ok {
			t.Errorf("%q não deveria ser lido", junk)
		}
	}
}

func TestScanKiroV3(t *testing.T) {
	root := t.TempDir()
	writeV3(t, root,
		v3Call("a", "git status", "null", "completed", time.Second), v3Result("a", time.Second+time.Millisecond),
		v3Call("b", "git push --force origin main", "null", "completed", 5*time.Second), v3Result("b", 6*time.Second), // SEM decisão (v3 não interativo)
		v3Call("c", "git push --force origin main", "null", "denied", 8*time.Second), v3Result("c", 8*time.Second), // negada: não executou
		v3Call("d", "git status", `"/outro/projeto"`, "completed", 9*time.Second), v3Result("d", 10*time.Second), // outro projeto
		v3Call("e", "git status", "null", "completed", 11*time.Second), // sem resultado ainda
	)
	// o hook grava a decisão milissegundos ANTES de o tool_call ser escrito.
	e := entry(kiroV3Session, "git status", time.Second-5*time.Millisecond)

	gaps, checked, covered, err := Scan([]string{root}, fmtSource(&KiroFormat, "/work/proj", e), t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || covered != 1 || len(gaps) != 1 || gaps[0].Class != "git push" || gaps[0].Call.Session != kiroV3Session {
		t.Errorf("conferidas %d, com decisão %d, lacunas %+v", checked, covered, gaps)
	}
}

// ---- Kiro v2

const kiroV2Session = "4f395751-d0e4-4739-80dc-8bfba9829c33"

func v2Line(kind, meta, content string) string {
	return `{"version":"v1","kind":"` + kind + `","data":{"message_id":"m",` + meta + `"content":[` + content + `]}}`
}

func v2Meta(at time.Duration) string {
	return `"meta":{"timestamp":` + strconv.FormatInt(t0.Add(at).Unix(), 10) + `},`
}

func v2Use(id, command, dir string) string {
	return `{"kind":"toolUse","data":{"toolUseId":"` + id + `","name":"shell","input":{"command":"` + command + `","working_dir":"` + dir + `","__tool_use_purpose":"x"}}}`
}

func v2Res(id string) string {
	return `{"kind":"toolResult","data":{"toolUseId":"` + id + `","content":[{"kind":"json","data":{"exit_status":"exit status: 1"}}],"status":"success"}}`
}

func TestKiroV2Parser(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cli", kiroV2Session+".jsonl")
	writeLines(t, path)
	parse := kiroParser(path)

	// o prompt traz o horário; a chamada vale a partir dele.
	if _, ok := parse([]byte(v2Line("Prompt", v2Meta(0), `{"kind":"text","data":"oi"}`))); ok {
		t.Error("o prompt não é chamada")
	}
	ev, ok := parse([]byte(v2Line("AssistantMessage", "", v2Use("tooluse_1", "git status", "/work/proj"))))
	if !ok || len(ev.calls) != 1 {
		t.Fatalf("deveria ler a chamada: %+v", ev)
	}
	c := ev.calls[0]
	if c.Session != kiroV2Session || c.Command != "git status" || c.Cwd != "/work/proj" || !c.At.Equal(t0) {
		t.Errorf("chamada inesperada: %+v", c)
	}
	if c.Slack != kiroV2Slack {
		t.Errorf("a chamada v2 tem a folga do prazo do hook: %v", c.Slack)
	}
	// sem horário de término: o resultado conta no horário da chamada.
	ev, ok = parse([]byte(v2Line("ToolResults", "", v2Res("tooluse_1"))))
	if !ok || !ev.results["tooluse_1"].Equal(t0) {
		t.Errorf("o resultado fecha a chamada no horário dela: %+v", ev)
	}
	// ferramenta que não é shell.
	other := v2Line("AssistantMessage", "", `{"kind":"toolUse","data":{"toolUseId":"z","name":"fs_read","input":{"path":"x"}}}`)
	if ev, ok := parse([]byte(other)); ok && len(ev.calls) != 0 {
		t.Errorf("fs_read não é shell: %+v", ev)
	}
}

func TestKiroV2WithoutAnyTimestampUsesCreatedAt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cli", kiroV2Session+".jsonl")
	writeLines(t, path)
	writeLines(t, filepath.Join(root, "cli", kiroV2Session+".json"), `{"session_id":"x","cwd":"/work/proj","created_at":"`+isoMs(0)+`"}`)

	ev, ok := kiroParser(path)([]byte(v2Line("AssistantMessage", "", v2Use("t", "ls", "/work/proj"))))
	if !ok || len(ev.calls) != 1 || !ev.calls[0].At.Equal(t0) {
		t.Errorf("sem horário na sessão vale o created_at do .json: %+v", ev)
	}
	// working_dir vazio (acontece no Kiro): vale o cwd do .json da sessão.
	ev, _ = kiroParser(path)([]byte(v2Line("AssistantMessage", "", v2Use("t2", "git status", ""))))
	if len(ev.calls) != 1 || ev.calls[0].Cwd != "/work/proj" {
		t.Errorf("working_dir vazio deveria usar o cwd da sessão: %+v", ev.calls)
	}
	// sem nenhuma referência de horário a chamada não pode ser conferida.
	bare := filepath.Join(root, "cli", "sem-meta.jsonl")
	writeLines(t, bare)
	if ev, ok := kiroParser(bare)([]byte(v2Line("AssistantMessage", "", v2Use("t", "ls", "/p")))); ok && len(ev.calls) != 0 {
		t.Errorf("sem horário nenhum não dá para conferir: %+v", ev)
	}
}

func TestScanKiroV2(t *testing.T) {
	root := t.TempDir()
	writeLines(t, filepath.Join(root, "cli", kiroV2Session+".jsonl"),
		v2Line("Prompt", v2Meta(0), `{"kind":"text","data":"x"}`),
		v2Line("AssistantMessage", "", v2Use("a", "git status", "/work/proj")),
		v2Line("ToolResults", "", v2Res("a")),
		v2Line("AssistantMessage", "", v2Use("b", "git push --force origin main", "/work/proj")), // SEM decisão
		v2Line("ToolResults", "", v2Res("b")),
		v2Line("AssistantMessage", "", v2Use("c", "git status", "/outro")), // outro projeto
		v2Line("ToolResults", "", v2Res("c")),
	)
	// o hook rodou 3 s depois do prompt (dentro do turno); o v2 não grava o término.
	gaps, checked, covered, err := Scan([]string{root}, fmtSource(&KiroFormat, "/work/proj", entry(kiroV2Session, "git status", 3*time.Second)), t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || covered != 1 || len(gaps) != 1 || gaps[0].Class != "git push" {
		t.Errorf("conferidas %d, com decisão %d, lacunas %+v", checked, covered, gaps)
	}
}

func TestKiroFilesListsBothVersions(t *testing.T) {
	root := t.TempDir()
	v3 := writeV3(t, root, v3Result("a", 0))
	v2 := filepath.Join(root, "cli", kiroV2Session+".jsonl")
	writeLines(t, v2)
	writeLines(t, filepath.Join(root, "cli", kiroV2Session+".json"), `{}`) // metadados: não é transcript
	writeLines(t, filepath.Join(root, "8e890988228762f5", kiroV3Session, "publish.cursor"), "1:2")

	files := kiroFiles(root)
	if len(files) != 2 || !(files[0] == v2 && files[1] == v3) {
		t.Errorf("só os transcripts dos dois formatos contam: %v", files)
	}
}

// ---- Codex

const codexSession = "01a0f9b2-1dc5-7573-972a-ca24ca6b643d"

func rolloutLine(kind, payload string, at time.Duration) string {
	return `{"timestamp":"` + isoMs(at) + `","type":"` + kind + `","payload":` + payload + `}`
}

func codexExec(callID, script string, at time.Duration) string {
	return rolloutLine("response_item", `{"type":"custom_tool_call","status":"completed","call_id":"`+callID+`","name":"exec","input":`+strconv.Quote(script)+`}`, at)
}

func codexOutput(callID string, at time.Duration) string {
	return rolloutLine("response_item", `{"type":"custom_tool_call_output","call_id":"`+callID+`","output":[{"type":"input_text","text":"Script completed"}]}`, at)
}

func writeRollout(t *testing.T, root string, lines ...string) string {
	t.Helper()
	path := filepath.Join(root, "2026", "10", "01", "rollout-2026-10-01T19-59-54-"+codexSession+".jsonl")
	writeLines(t, path, lines...)
	return path
}

func TestCodexCommands(t *testing.T) {
	cases := []struct {
		name, script string
		want         []string
	}{
		{"formato real", "const r = await tools.exec_command({cmd:\"echo oi\"});\ntext(r.output);\n", []string{"echo oi"}},
		{"com outras chaves antes e depois", `const r = await tools.exec_command({yield_time_ms:10000,cmd:"git push --force origin main",max_output_tokens:2000});`, []string{"git push --force origin main"}},
		{"escapes JSON", `await tools.exec_command({cmd:"echo \"a b\" && echo 'c'\nls"});`, []string{"echo \"a b\" && echo 'c'\nls"}},
		{"aspas simples", `await tools.exec_command({cmd:'git status'});`, []string{"git status"}},
		{"template sem interpolação", "await tools.exec_command({cmd:`ls -la`});", []string{"ls -la"}},
		{"dois comandos", `await tools.exec_command({cmd:"a"}); await tools.exec_command({cmd:"b"});`, []string{"a", "b"}},
		// o que não dá para ler sem risco fica de fora.
		{"variável", `const c = "rm -rf x"; await tools.exec_command({cmd:c});`, nil},
		{"concatenação (nunca ler pela metade)", `await tools.exec_command({cmd:"rm " + "-rf x"});`, nil},
		{"literal e depois fim do objeto", `await tools.exec_command({cmd:"ls" });`, []string{"ls"}},
		{"template com interpolação", "await tools.exec_command({cmd:`rm -rf ${d}`});", nil},
		{"escape que não conheço", `await tools.exec_command({cmd:'a\x41'});`, nil},
		{"outra ferramenta", `await tools.apply_patch({patch:"x"});`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := codexCommands(c.script)
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("codexCommands = %q, quero %q", got, c.want)
			}
		})
	}
}

func TestCodexParserRealShapes(t *testing.T) {
	root := t.TempDir()
	path := writeRollout(t, root)
	parse := codexParser(path)

	if _, ok := parse([]byte(rolloutLine("session_meta", `{"id":"`+codexSession+`","cwd":"/work/proj"}`, 0))); ok {
		t.Error("session_meta não é chamada")
	}
	ev, ok := parse([]byte(codexExec("call_1", `const r = await tools.exec_command({cmd:"git status"});`, time.Second)))
	if !ok || len(ev.calls) != 1 {
		t.Fatalf("deveria ler a chamada: %+v", ev)
	}
	c := ev.calls[0]
	if c.Session != codexSession || c.Command != "git status" || c.Cwd != "/work/proj" || c.ID != "call_1#0" || !c.At.Equal(t0.Add(time.Second)) {
		t.Errorf("chamada inesperada: %+v", c)
	}
	// o cwd acompanha o turn_context.
	parse([]byte(rolloutLine("turn_context", `{"cwd":"/work/proj/sub"}`, 2*time.Second)))
	if ev, _ := parse([]byte(codexExec("call_2", `await tools.exec_command({cmd:"ls"});`, 3*time.Second))); ev.calls[0].Cwd != "/work/proj/sub" {
		t.Errorf("o cwd deveria seguir o turn_context: %+v", ev.calls)
	}
	// o resultado fecha todos os comandos do script.
	parse([]byte(codexExec("call_3", `await tools.exec_command({cmd:"a"}); await tools.exec_command({cmd:"b"});`, 4*time.Second)))
	ev, _ = parse([]byte(codexOutput("call_3", 5*time.Second)))
	if len(ev.results) != 2 || !ev.results["call_3#1"].Equal(t0.Add(5*time.Second)) {
		t.Errorf("o resultado fecha os dois comandos: %+v", ev.results)
	}
	// wait e outros itens não são chamadas.
	if ev, ok := parse([]byte(rolloutLine("response_item", `{"type":"function_call","name":"wait","call_id":"w","arguments":"{}"}`, 0))); ok {
		t.Errorf("wait não é shell: %+v", ev)
	}
	if _, ok := parse([]byte("isto não é json")); ok {
		t.Error("lixo não deveria ser lido")
	}
}

func TestScanCodex(t *testing.T) {
	root := t.TempDir()
	writeRollout(t, root,
		rolloutLine("session_meta", `{"id":"`+codexSession+`","cwd":"/work/proj"}`, 0),
		codexExec("c1", `const r = await tools.exec_command({cmd:"git status"});`, time.Second), codexOutput("c1", 2*time.Second),
		codexExec("c2", `const r = await tools.exec_command({cmd:"git push --force origin main"});`, 5*time.Second), codexOutput("c2", 6*time.Second), // SEM decisão
		codexExec("c3", `const c = "ls"; await tools.exec_command({cmd:c});`, 8*time.Second), codexOutput("c3", 9*time.Second), // não dá para ler
		codexExec("c4", `await tools.exec_command({cmd:"git status"});`, 11*time.Second), // sem resultado ainda
	)
	gaps, checked, covered, err := Scan([]string{root}, fmtSource(&CodexFormat, "/work/proj", entry(codexSession, "git status", time.Second+300*time.Millisecond)), t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || covered != 1 || len(gaps) != 1 || gaps[0].Class != "git push" || gaps[0].Call.Session != codexSession {
		t.Errorf("conferidas %d, com decisão %d, lacunas %+v", checked, covered, gaps)
	}
	// outro projeto fica de fora.
	_, checked, _, _ = Scan([]string{root}, fmtSource(&CodexFormat, "/outro", entry(codexSession, "git status", 0)), t0.Add(-time.Hour))
	if checked != 0 {
		t.Errorf("de outro projeto não conta: %d", checked)
	}
}

func TestCodexFilesAndSessionsDirs(t *testing.T) {
	root := t.TempDir()
	good := writeRollout(t, root)
	writeLines(t, filepath.Join(root, "2026", "10", "01", "notas.jsonl"), "{}")
	if files := codexFiles(root); len(files) != 1 || files[0] != good {
		t.Errorf("só rollout-*.jsonl conta: %v", files)
	}

	t.Setenv("CODEX_HOME", "")
	if got := CodexSessionsDir("/home/u"); got != filepath.Join("/home/u", ".codex", "sessions") {
		t.Errorf("padrão: %q", got)
	}
	t.Setenv("CODEX_HOME", "/x/ch")
	if got := CodexSessionsDir("/home/u"); got != filepath.Join("/x/ch", "sessions") {
		t.Errorf("CODEX_HOME: %q", got)
	}
	if got := KiroSessionsDir("/home/u"); got != filepath.Join("/home/u", ".kiro", "sessions") {
		t.Errorf("kiro: %q", got)
	}
}

// Regressão (horários reais do Codex, 2026-10-01): no codex exec o resultado da
// chamada foi gravado 31 s depois dela, e a decisão do hook (janela esperando o
// clique até os 45 s) só às 45 s. Não pode virar lacuna.
func TestScanCodexDecisionAfterTheResultWasRecorded(t *testing.T) {
	root := t.TempDir()
	writeRollout(t, root,
		rolloutLine("session_meta", `{"id":"`+codexSession+`","cwd":"/work/proj"}`, 0),
		codexExec("c1", `const r = await tools.exec_command({cmd:"git push --force-with-lease origin main"});`, time.Second),
		codexOutput("c1", 32*time.Second),
	)
	late := audit.Entry{Time: t0.Add(46 * time.Second), Session: codexSession, Class: "git push", Decision: "deny", Agent: "codex"}

	_, checked, covered, err := Scan([]string{root}, fmtSource(&CodexFormat, "/work/proj", late), t0.Add(-time.Hour))
	if err != nil || checked != 1 || covered != 1 {
		t.Errorf("a decisão gravada até 60 s depois do resultado cobre a chamada: %d %d %v", checked, covered, err)
	}

	// passou do prazo: é lacuna.
	tooLate := late
	tooLate.Time = t0.Add(32*time.Second + 2*time.Second + 61*time.Second)
	if _, _, covered, _ := Scan([]string{root}, fmtSource(&CodexFormat, "/work/proj", tooLate), t0.Add(-time.Hour)); covered != 0 {
		t.Error("uma decisão muito depois do resultado não cobre a chamada")
	}

}

func TestTrackerWaitsForTheSlackBeforeAccusing(t *testing.T) {
	src := fmtSource(&CodexFormat, "/work/proj")
	tr := NewTracker(src, 3*time.Second)
	tr.Add(event{
		calls:   []Call{{Session: codexSession, ID: "c1", Command: "git push --force origin main", Cwd: "/work/proj", At: t0, Slack: codexSlack}},
		results: map[string]time.Time{"c1": t0.Add(10 * time.Second)},
	})
	// 4 s depois do resultado ainda pode chegar a decisão (folga de 60 s).
	if gaps, _ := tr.Check(t0.Add(14 * time.Second)); len(gaps) != 0 {
		t.Errorf("ainda é cedo para acusar: %+v", gaps)
	}
	if gaps, _ := tr.Check(t0.Add(10*time.Second + 64*time.Second)); len(gaps) != 1 {
		t.Errorf("passada a folga, é lacuna: %+v", gaps)
	}
}

// O Kiro v2 não grava horário de término: a decisão (janela esperando o clique)
// pode vir minutos depois do prompt e ainda cobre a chamada.
func TestScanKiroV2DecisionMinutesLater(t *testing.T) {
	root := t.TempDir()
	writeLines(t, filepath.Join(root, "cli", kiroV2Session+".jsonl"),
		v2Line("Prompt", v2Meta(0), `{"kind":"text","data":"x"}`),
		v2Line("AssistantMessage", "", v2Use("a", "git push --force-with-lease origin main", "/work/proj")),
		v2Line("ToolResults", "", v2Res("a")),
	)
	late := audit.Entry{Time: t0.Add(4 * time.Minute), Session: kiroV2Session, Class: "git push", Decision: "userApproved", Agent: "kiro"}
	if _, checked, covered, err := Scan([]string{root}, fmtSource(&KiroFormat, "/work/proj", late), t0.Add(-time.Hour)); err != nil || checked != 1 || covered != 1 {
		t.Errorf("a decisão dentro do prazo do hook cobre a chamada: %d %d %v", checked, covered, err)
	}
	tooLate := late
	tooLate.Time = t0.Add(11 * time.Minute)
	if _, _, covered, _ := Scan([]string{root}, fmtSource(&KiroFormat, "/work/proj", tooLate), t0.Add(-time.Hour)); covered != 0 {
		t.Error("depois do prazo do hook não cobre")
	}
}

// Quem não tem folga (Slack zero) não aceita decisão depois do resultado.
func TestNoSlackMeansNoLateDecisions(t *testing.T) {
	src := fmtSource(&KiroFormat, "/work/proj", audit.Entry{Time: t0.Add(30 * time.Second), Session: kiroV3Session, Class: "git push", Decision: "deny"})
	tr := NewTracker(src, 0)
	tr.Add(event{
		calls:   []Call{{Session: kiroV3Session, ID: "x", Command: "git push --force origin main", Cwd: "/work/proj", At: t0}},
		results: map[string]time.Time{"x": t0.Add(time.Second)},
	})
	if gaps, _ := tr.Check(t0.Add(time.Hour)); len(gaps) != 1 {
		t.Errorf("sem folga uma decisão 30 s depois não cobre: %+v", gaps)
	}
}
