package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A amostra é o stdin real do Copilot CLI 1.0.112 (interativo), capturado em
// 2026-11-15; só caminhos e identificadores foram trocados.
func TestCopilotParseEventRealSample(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "events", "copilot", "bash-interativo.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev, err := Copilot.ParseEvent(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := Event{
		Command: "echo iron-copilot-ok", Tool: "bash", Shell: true,
		SessionID: "62b46c1b-54d4-4521-ae91-998e15cfeb59", Cwd: "/home/dev/projeto",
	}
	if ev != want {
		t.Errorf("evento inesperado: %+v", ev)
	}
}

func TestCopilotParseEventVariants(t *testing.T) {
	cases := []struct {
		name, in string
		want     Event
	}{
		{"powershell", `{"toolName":"powershell","toolArgs":{"command":"Get-Date"},"cwd":"C:\\p"}`,
			Event{Command: "Get-Date", Tool: "powershell", Shell: true, PowerShell: true, Cwd: `C:\p`}},
		{"toolArgs como texto JSON", `{"toolName":"bash","toolArgs":"{\"command\":\"ls\"}"}`,
			Event{Command: "ls", Tool: "bash", Shell: true}},
		// outra ferramenta nunca vira comando, mesmo que traga "command".
		{"não é shell", `{"toolName":"edit","toolArgs":{"command":"rm -rf /","path":"a"}}`,
			Event{Tool: "edit"}},
		{"sem toolArgs", `{"toolName":"ask_user"}`, Event{Tool: "ask_user"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, err := Copilot.ParseEvent(strings.NewReader(c.in))
			if err != nil || ev != c.want {
				t.Errorf("evento %+v, erro %v; esperava %+v", ev, err, c.want)
			}
		})
	}
}

func TestCopilotParseEventBroken(t *testing.T) {
	for name, in := range map[string]string{
		"vazio":               "",
		"quebrado":            "{",
		"comando não é texto": `{"toolName":"bash","toolArgs":{"command":5}}`,
		"toolArgs inválido":   `{"toolName":"bash","toolArgs":"não é json"}`,
	} {
		if _, err := Copilot.ParseEvent(strings.NewReader(in)); err == nil {
			t.Errorf("%s deveria ser erro, para o hook bloquear", name)
		}
	}
}

func TestCopilotRespond(t *testing.T) {
	cases := []struct {
		name     string
		decision Decision
		wantDeny bool
	}{
		{"allow", Allow, false},
		{"userApproved", UserApproved, false},
		{"deny", Deny, true},
		// sem verificação do ask no agente real, ele nunca pode virar allow.
		{"ask", Ask, true},
		{"desconhecida", Decision("talvez"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Copilot.Respond(&stdout, &stderr, c.decision, "bloqueado <agora>")
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("código %d, stderr %q", code, stderr.String())
			}
			if !c.wantDeny {
				if stdout.Len() != 0 {
					t.Errorf("liberar não escreve nada, obtive %q", stdout.String())
				}
				return
			}
			var out map[string]string
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatalf("saída não é JSON: %v (%q)", err, stdout.String())
			}
			if out["permissionDecision"] != "deny" || out["permissionDecisionReason"] == "" {
				t.Errorf("deny inesperado: %v", out)
			}
			// o motivo não pode sair escapado (<) no que o modelo lê.
			if c.decision == Deny && !strings.Contains(stdout.String(), "<agora>") {
				t.Errorf("o motivo saiu escapado: %q", stdout.String())
			}
		})
	}
}

// Sem como escrever o JSON, o deny cai no exit 2 (todo código ≠ 0 bloqueia).
func TestCopilotRespondFallsBackToExit2(t *testing.T) {
	var stderr bytes.Buffer
	if code := Copilot.Respond(failingWriter{}, &stderr, Deny, "bloqueado"); code != 2 || stderr.String() != "bloqueado\n" {
		t.Errorf("código %d, stderr %q", code, stderr.String())
	}
}

func TestCopilotCapabilities(t *testing.T) {
	c := Copilot.Capabilities()
	if c.CanAsk || c.Timeout != CopilotTimeout || c.AppID != "iron-copilot" {
		t.Errorf("capacidades inesperadas: %+v", c)
	}
}

func TestCopilotLookup(t *testing.T) {
	if a, err := Lookup("COPILOT"); err != nil || a.Name() != "copilot" {
		t.Errorf("Lookup(COPILOT) = %v, %v", a, err)
	}
}
