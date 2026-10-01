package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// As amostras são o stdin real do Kiro CLI 2.26.1 (v2 e v3), capturado em
// 2026-10-01; só o caminho do projeto foi trocado.
func TestKiroParseEventRealSamples(t *testing.T) {
	cases := []struct {
		file      string
		wantTool  string
		wantSess  string
		wantShell bool
	}{
		{"v2-shell.json", "shell", "e075b302-baae-491a-b9f0-6a936d898630", true},
		{"v3-execute_bash.json", "execute_bash", "sess_34ca342c-4ef7-4a12-9467-b5d874a946a1", true},
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "events", "kiro", c.file))
			if err != nil {
				t.Fatal(err)
			}
			ev, err := Kiro.ParseEvent(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if ev.Command != "echo oi" || ev.Tool != c.wantTool || ev.Shell != c.wantShell ||
				ev.SessionID != c.wantSess || ev.Cwd != "/home/dev/projeto" {
				t.Errorf("evento inesperado: %+v", ev)
			}
		})
	}
}

func TestKiroParseEventNonShellAndBroken(t *testing.T) {
	ev, err := Kiro.ParseEvent(strings.NewReader(`{"tool_name":"fs_read","tool_input":{"path":"x"},"cwd":"/p"}`))
	if err != nil || ev.Shell || ev.Command != "" {
		t.Errorf("ferramenta que não é shell: %+v, %v", ev, err)
	}

	// tool_input.cwd do modelo não pode mudar a pasta usada para a política.
	ev, err = Kiro.ParseEvent(strings.NewReader(`{"tool_name":"execute_bash","cwd":"/p","tool_input":{"command":"ls","cwd":"/tmp"}}`))
	if err != nil || ev.Cwd != "/p" {
		t.Errorf("cwd do tool_input deve ser ignorado: %+v, %v", ev, err)
	}

	if _, err := Kiro.ParseEvent(strings.NewReader("")); err == nil {
		t.Error("stdin vazio (o Kiro IDE) deve ser erro, para o hook bloquear")
	}
	if _, err := Kiro.ParseEvent(strings.NewReader("{")); err == nil {
		t.Error("JSON quebrado deve ser erro")
	}
}

func TestKiroRespond(t *testing.T) {
	cases := []struct {
		name       string
		decision   Decision
		wantStderr string
		wantCode   int
	}{
		{"allow", Allow, "", 0},
		{"userApproved", UserApproved, "", 0},
		// Só a saída 2 bloqueia no Kiro; o stderr vai ao modelo.
		{"deny", Deny, "bloqueado\n", 2},
		// O Kiro ignora o JSON de ask: nunca pode virar allow.
		{"ask", Ask, "bloqueado\n", 2},
		{"desconhecida", Decision("talvez"), "iron: decisão desconhecida \"talvez\"\n", 2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Kiro.Respond(&stdout, &stderr, c.decision, "bloqueado")
			if code != c.wantCode || stderr.String() != c.wantStderr {
				t.Errorf("código %d, stderr %q", code, stderr.String())
			}
			// JSON no stdout é ignorado pelo Kiro: nunca escrever.
			if stdout.Len() != 0 {
				t.Errorf("stdout deve ficar vazio, obtive %q", stdout.String())
			}
		})
	}
}

func TestKiroCapabilities(t *testing.T) {
	c := Kiro.Capabilities()
	if c.CanAsk {
		t.Error("o Kiro não entende ask (verificado com o agente real)")
	}
	if c.Timeout != KiroTimeout || c.Budget().DialogWait <= 0 {
		t.Errorf("o prazo gravado precisa dar tempo à janela: %+v %+v", c, c.Budget())
	}
}

func TestKiroLookup(t *testing.T) {
	a, err := Lookup("KIRO")
	if err != nil || a.Name() != "kiro" {
		t.Errorf("Lookup(KIRO) = %v, %v", a, err)
	}
}
