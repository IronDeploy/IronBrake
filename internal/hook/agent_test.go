package hook

import (
	"strings"
	"testing"
	"time"
)

func TestLookup(t *testing.T) {
	for _, name := range []string{"claude", "Claude", "CLAUDE"} {
		a, err := Lookup(name)
		if err != nil || a.Name() != "claude" {
			t.Errorf("Lookup(%q) = %v, %v", name, a, err)
		}
	}

	_, err := Lookup("emacs")
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Errorf("agente desconhecido deve falhar listando os suportados, obtive %v", err)
	}
}

func TestDefaultAgentExists(t *testing.T) {
	if _, err := Lookup(DefaultAgent); err != nil {
		t.Fatal(err)
	}
}

func TestProjectDir(t *testing.T) {
	env := map[string]string{"CLAUDE_PROJECT_DIR": "/p"}
	get := func(k string) string { return env[k] }

	if got := ProjectDir(get); got != "/p" {
		t.Errorf("esperava /p, obtive %q", got)
	}
	if got := ProjectDir(func(string) string { return "" }); got != "" {
		t.Errorf("sem variável deve devolver vazio, obtive %q", got)
	}
}

func TestClaudeParseEventRejectsBadJSON(t *testing.T) {
	if _, err := Claude.ParseEvent(strings.NewReader("{")); err == nil {
		t.Error("JSON inválido deve dar erro")
	}
}

func TestBudget(t *testing.T) {
	cases := []struct {
		name         string
		timeout      time.Duration
		wantDeadline time.Duration
		wantDialog   time.Duration
	}{
		// Os valores de sempre do Claude Code: 600 s → 560 s, janela de 480 s.
		{"600 s", 600 * time.Second, 560 * time.Second, 480 * time.Second},
		{"120 s", 120 * time.Second, 112 * time.Second, 32 * time.Second},
		// Sem tempo para a janela (Gemini 60 s, Copilot 30 s): sem janela.
		{"60 s", 60 * time.Second, 56 * time.Second, 0},
		{"30 s", 30 * time.Second, 28 * time.Second, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Capabilities{Timeout: c.timeout}.Budget()
			if got.Deadline != c.wantDeadline || got.DialogWait != c.wantDialog {
				t.Errorf("esperava %v/%v, obtive %v/%v", c.wantDeadline, c.wantDialog, got.Deadline, got.DialogWait)
			}
			if got.Deadline >= c.timeout {
				t.Errorf("o prazo do hook (%v) precisa ser menor que o do agente (%v)", got.Deadline, c.timeout)
			}
		})
	}
}

// O prazo do Claude é uma constante que o doctor usa: as duas contas têm de
// concordar.
func TestClaudeBudgetMatchesDeadline(t *testing.T) {
	caps := Claude.Capabilities()
	if got := caps.Budget().Deadline; got != Deadline {
		t.Errorf("Budget().Deadline = %v, mas hook.Deadline = %v", got, Deadline)
	}
	if !caps.CanAsk {
		t.Error("o Claude Code entende ask")
	}
	if caps.Timeout < MinTimeout {
		t.Errorf("Timeout %v abaixo de MinTimeout %v", caps.Timeout, MinTimeout)
	}
}
