package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// As amostras são o stdin real do Codex CLI 0.159.3 (exec e chat), capturado em
// 2026-10-01; só caminhos e identificadores foram trocados.
func TestCodexParseEventRealSamples(t *testing.T) {
	cases := []struct {
		file      string
		wantShell bool
		wantTool  string
	}{
		{"bash-exec.json", true, "Bash"},
		{"bash-interativo.json", true, "Bash"},
		// o texto do patch vem em tool_input.command, mas não é shell.
		{"apply_patch.json", false, "apply_patch"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "events", "codex", c.file))
			if err != nil {
				t.Fatal(err)
			}
			ev, err := Codex.ParseEvent(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if ev.Tool != c.wantTool || ev.Shell != c.wantShell || ev.Cwd != "/home/dev/projeto" ||
				ev.SessionID != "01a0f9b2-1dc5-7573-972a-ca24ca6b643d" || ev.Command == "" {
				t.Errorf("evento inesperado: %+v", ev)
			}
		})
	}
}

func TestCodexParseEventBroken(t *testing.T) {
	for name, in := range map[string]string{"vazio": "", "quebrado": "{", "tipo errado": `{"tool_input":{"command":5}}`} {
		if _, err := Codex.ParseEvent(strings.NewReader(in)); err == nil {
			t.Errorf("%s deveria ser erro, para o hook bloquear", name)
		}
	}
}

func TestCodexRespond(t *testing.T) {
	cases := []struct {
		name       string
		decision   Decision
		wantStderr string
		wantCode   int
	}{
		// sem saída: um JSON "allow" é falha do hook no Codex (e o comando executa).
		{"allow", Allow, "", 0},
		{"userApproved", UserApproved, "", 0},
		{"deny", Deny, "bloqueado\n", 2},
		// ask é "unsupported" no Codex: nunca pode virar allow.
		{"ask", Ask, "bloqueado\n", 2},
		{"desconhecida", Decision("talvez"), "iron: decisão desconhecida \"talvez\"\n", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Codex.Respond(&stdout, &stderr, c.decision, "bloqueado")
			if code != c.wantCode || stderr.String() != c.wantStderr || stdout.Len() != 0 {
				t.Errorf("código %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCodexCapabilitiesCapTheDialog(t *testing.T) {
	c := Codex.Capabilities()
	if c.CanAsk || c.Timeout != CodexTimeout || c.AppID != "iron-codex" {
		t.Errorf("capacidades inesperadas: %+v", c)
	}
	// o prazo gravado dá 480 s à janela, mas o Codex abandona hooks longos no exec: o teto vale.
	if b := c.Budget(); b.DialogWait != CodexDialogCap || b.Deadline <= CodexDialogCap {
		t.Errorf("a janela deveria esperar no máximo %s: %+v", CodexDialogCap, b)
	}
}

func TestDialogCap(t *testing.T) {
	base := Capabilities{Timeout: 600 * time.Second}
	if w := base.Budget().DialogWait; w != 480*time.Second {
		t.Errorf("sem teto: 480 s, obtive %s", w)
	}
	capped := Capabilities{Timeout: 600 * time.Second, DialogCap: 30 * time.Second}
	if w := capped.Budget().DialogWait; w != 30*time.Second {
		t.Errorf("com teto de 30 s, obtive %s", w)
	}
	// teto maior que o disponível não aumenta a espera; sem espaço para janela continua zero.
	uncapped := Capabilities{Timeout: 100 * time.Second}.Budget().DialogWait
	if w := (Capabilities{Timeout: 100 * time.Second, DialogCap: 600 * time.Second}).Budget().DialogWait; w != uncapped || w <= 0 {
		t.Errorf("o teto não aumenta a espera: %s (sem teto: %s)", w, uncapped)
	}
	if w := (Capabilities{Timeout: 30 * time.Second, DialogCap: 20 * time.Second}).Budget().DialogWait; w != 0 {
		t.Errorf("sem espaço para a janela: %s", w)
	}
}

func TestCodexLookup(t *testing.T) {
	if a, err := Lookup("CODEX"); err != nil || a.Name() != "codex" {
		t.Errorf("Lookup(CODEX) = %v, %v", a, err)
	}
}
