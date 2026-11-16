package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// As amostras são o stdin real do Cursor (cursor-agent 2026.11.13 e o app 3.30.14),
// capturado em 2026-11-16; só pasta, e-mail e identificadores foram trocados.
func TestCursorParseEventRealSamples(t *testing.T) {
	cases := []struct {
		file    string
		session string
		command string
		tool    string
	}{
		{"shell-cli.json", "5f659ccc-92b5-4862-b9a1-429297781961", "echo iron-cursor-ok", "Shell"},
		{"shell-ide.json", "aada8690-ff44-472c-a78c-d195fb3d1555", "echo iron-cursor-ide", "Shell"},
		{"pretooluse-ide.json", "aada8690-ff44-472c-a78c-d195fb3d1555", "echo iron-cursor-ide", "Shell"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "events", "cursor", c.file))
			if err != nil {
				t.Fatal(err)
			}
			ev, err := Cursor.ParseEvent(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			// o "cwd" chega vazio: vale a raiz do projeto.
			want := Event{Command: c.command, Tool: c.tool, Shell: true, SessionID: c.session, Cwd: "/home/dev/projeto"}
			if ev != want {
				t.Errorf("evento inesperado: %+v", ev)
			}
		})
	}
}

func TestCursorParseEventVariants(t *testing.T) {
	cases := []struct {
		name, in string
		want     Event
	}{
		{"cwd do evento vence", `{"command":"ls","cwd":"/p","workspace_roots":["/outra"]}`,
			Event{Command: "ls", Tool: "Shell", Shell: true, Cwd: "/p"}},
		{"sem cwd nem raiz", `{"command":"ls"}`, Event{Command: "ls", Tool: "Shell", Shell: true}},
		{"conversation_id sem session_id", `{"command":"ls","conversation_id":"c1"}`,
			Event{Command: "ls", Tool: "Shell", Shell: true, SessionID: "c1"}},
		{"preToolUse com cwd da ferramenta", `{"tool_name":"Shell","tool_input":{"command":"ls","cwd":"/t"}}`,
			Event{Command: "ls", Tool: "Shell", Shell: true, Cwd: "/t"}},
		// outra ferramenta nunca vira comando, mesmo que traga "command".
		{"não é shell", `{"tool_name":"Write","tool_input":{"command":"rm -rf /"}}`, Event{Tool: "Write"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, err := Cursor.ParseEvent(strings.NewReader(c.in))
			if err != nil || ev != c.want {
				t.Errorf("evento %+v, erro %v; esperava %+v", ev, err, c.want)
			}
		})
	}
}

func TestCursorParseEventBroken(t *testing.T) {
	for name, in := range map[string]string{
		"vazio":               "",
		"quebrado":            "{",
		"comando não é texto": `{"command":5}`,
		"ferramenta inválida": `{"tool_name":5}`,
	} {
		if _, err := Cursor.ParseEvent(strings.NewReader(in)); err == nil {
			t.Errorf("%s deveria ser erro, para o hook bloquear", name)
		}
	}
}

func TestCursorRespond(t *testing.T) {
	cases := []struct {
		name     string
		decision Decision
		want     string
	}{
		// com failClosed, saída vazia bloqueia: o allow tem de ser explícito.
		{"allow", Allow, "allow"},
		{"userApproved", UserApproved, "allow"},
		{"deny", Deny, "deny"},
		// o ask do hook é aprovado por --force: nunca pode ser allow.
		{"ask", Ask, "deny"},
		{"desconhecida", Decision("talvez"), "deny"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Cursor.Respond(&stdout, &stderr, c.decision, "bloqueado <agora>"); code != 0 || stderr.Len() != 0 {
				t.Fatalf("código %d, stderr %q", code, stderr.String())
			}
			var out map[string]string
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatalf("saída não é JSON: %v (%q)", err, stdout.String())
			}
			if out["permission"] != c.want {
				t.Fatalf("permission %q; esperava %q", out["permission"], c.want)
			}
			if c.want == "allow" {
				if len(out) != 1 {
					t.Errorf("allow não leva mensagem: %v", out)
				}
				return
			}
			if out["user_message"] == "" || out["agent_message"] == "" {
				t.Errorf("deny sem mensagem: %v", out)
			}
			if c.decision == Deny && !strings.Contains(stdout.String(), "<agora>") {
				t.Errorf("o motivo saiu escapado: %q", stdout.String())
			}
		})
	}
}

// Sem como escrever o JSON, tanto o deny quanto o allow viram exit 2 (bloqueio).
func TestCursorRespondFallsBackToExit2(t *testing.T) {
	var stderr bytes.Buffer
	if code := Cursor.Respond(failingWriter{}, &stderr, Deny, "bloqueado"); code != 2 || stderr.String() != "bloqueado\n" {
		t.Errorf("deny: código %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := Cursor.Respond(failingWriter{}, &stderr, Allow, ""); code != 2 || stderr.Len() == 0 {
		t.Errorf("allow sem saída deveria bloquear: código %d, stderr %q", code, stderr.String())
	}
}

func TestCursorCapabilities(t *testing.T) {
	c := Cursor.Capabilities()
	if c.CanAsk || c.Timeout != CursorTimeout || c.AppID != "iron-cursor" {
		t.Errorf("capacidades inesperadas: %+v", c)
	}
}

func TestCursorLookup(t *testing.T) {
	if a, err := Lookup("CURSOR"); err != nil || a.Name() != "cursor" {
		t.Errorf("Lookup(CURSOR) = %v, %v", a, err)
	}
}
