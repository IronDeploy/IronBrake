package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// As amostras são o stdin real do Antigravity CLI 1.2.14 (modo interativo e
// -p), capturado em 2026-10-01; só caminhos e identificadores foram trocados.
func TestAntigravityParseEventRealSamples(t *testing.T) {
	for _, file := range []string{"run_command-interativo.json", "run_command-print.json"} {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "events", "antigravity", file))
			if err != nil {
				t.Fatal(err)
			}
			ev, err := Antigravity.ParseEvent(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if ev.Command != "echo oi" || ev.Tool != "run_command" || !ev.Shell || ev.Cwd != "/home/dev/projeto" ||
				len(ev.SessionID) != 36 {
				t.Errorf("evento inesperado: %+v", ev)
			}
		})
	}
}

func TestAntigravityParseEventOtherTools(t *testing.T) {
	ev, err := Antigravity.ParseEvent(strings.NewReader(`{"toolCall":{"name":"view_file","args":{"AbsolutePath":"/x"}},"workspacePaths":["/p"]}`))
	if err != nil || ev.Shell || ev.Command != "" {
		t.Errorf("view_file não é shell: %+v, %v", ev, err)
	}
	for name, in := range map[string]string{"vazio": "", "quebrado": "{", "tipo errado": `{"toolCall":{"args":{"CommandLine":5}}}`} {
		if _, err := Antigravity.ParseEvent(strings.NewReader(in)); err == nil {
			t.Errorf("%s deveria ser erro, para o hook bloquear", name)
		}
	}
}

func TestAntigravityCwd(t *testing.T) {
	cases := []struct {
		name, cwd string
		ws        []string
		want      string
	}{
		{"na raiz", "/p", []string{"/p"}, "/p"},
		{"subpasta vale a raiz do workspace", "/p/sub/x", []string{"/p"}, "/p"},
		{"com barra no fim e ponto", "/p/sub/../sub/", []string{"/p/"}, "/p"},
		{"prefixo parecido não é subpasta", "/p2", []string{"/p"}, "/p2"},
		{"fora do workspace vale o Cwd", "/tmp/a/../b", []string{"/p"}, "/tmp/b"},
		{"pai do workspace é fora", "/", []string{"/p"}, "/"},
		{"segundo workspace", "/q/z", []string{"/p", "/q"}, "/q"},
		{"sem Cwd usa o workspace", "", []string{"/p"}, "/p"},
		{"sem nada", "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := antigravityCwd(c.cwd, c.ws); got != c.want {
				t.Errorf("antigravityCwd(%q, %v) = %q, quero %q", c.cwd, c.ws, got, c.want)
			}
		})
	}
}

func TestAntigravityRespond(t *testing.T) {
	cases := []struct {
		name       string
		decision   Decision
		wantStdout string
		wantStderr string
		wantCode   int
	}{
		// "sem opinião": nada no stdout (um "{}" seria deny).
		{"allow", Allow, "", "", 0},
		{"userApproved", UserApproved, "", "", 0},
		{"deny", Deny, `{"decision":"deny","reason":"motivo <x> & y"}` + "\n", "", 0},
		// ask nunca vira allow: com aprovação automática o agente executaria.
		{"ask", Ask, `{"decision":"deny","reason":"motivo <x> & y"}` + "\n", "", 0},
		{"desconhecida", Decision("talvez"), `{"decision":"deny","reason":"iron: decisão desconhecida \"talvez\""}` + "\n", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Antigravity.Respond(&stdout, &stderr, c.decision, "motivo <x> & y")
			if code != c.wantCode || stdout.String() != c.wantStdout || stderr.String() != c.wantStderr {
				t.Errorf("código %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestAntigravityRespondWriteFailureStillBlocks(t *testing.T) {
	var stderr bytes.Buffer
	if code := Antigravity.Respond(failingWriter{}, &stderr, Deny, "bloqueado"); code != 2 || !strings.Contains(stderr.String(), "bloqueado") {
		t.Errorf("sem poder escrever o JSON, deveria sair 2 com o motivo: %d %q", code, stderr.String())
	}
}

func TestAntigravityCapabilitiesAndLookup(t *testing.T) {
	c := Antigravity.Capabilities()
	if c.CanAsk || c.Timeout != AntigravityTimeout || c.Budget().DialogWait <= 0 {
		t.Errorf("capacidades inesperadas: %+v %+v", c, c.Budget())
	}
	for _, name := range []string{"antigravity", "AGY", "Agy"} {
		if a, err := Lookup(name); err != nil || a.Name() != "antigravity" {
			t.Errorf("Lookup(%q) = %v, %v", name, a, err)
		}
	}
}
