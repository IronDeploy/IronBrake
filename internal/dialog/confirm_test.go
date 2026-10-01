package dialog

import (
	"errors"
	"testing"
	"time"
)

func TestParseAnswer(t *testing.T) {
	// Saídas do script. "Cancelar" foi observado clicando na janela real
	// (macOS pt_BR); antes da correção, o clique virava erro -128 e caía em
	// Unavailable, e a caixa do Claude Code aparecia no lugar do deny.
	cases := []struct {
		name   string
		output string
		err    error
		want   Answer
	}{
		{"clicou em Executar", "Executar\n", nil, Approved},
		{"clicou em Cancelar (ou Esc)", "Cancelar\n", nil, Rejected},
		{"tempo esgotado", "timeout\n", nil, Rejected},
		// Só a resposta exata aprova; qualquer coisa estranha é recusa.
		{"saída vazia", "", nil, Rejected},
		{"Executar com texto a mais", "Executar, gave up:false\n", nil, Rejected},
		{"nome antigo do botão", "Aplicar\n", nil, Rejected},
		// O osascript falhou (sem tela, SSH): não deu para perguntar.
		{"osascript falhou", "", errors.New("exit status 1"), Unavailable},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseAnswer(c.output, c.err); got != c.want {
				t.Errorf("esperava %v, obtive %v", c.want, got)
			}
		})
	}
}

func TestConfirmUsesSystemOsascript(t *testing.T) {
	if osascriptPath != "/usr/bin/osascript" {
		t.Errorf("esperava /usr/bin/osascript, obtive %q", osascriptPath)
	}
}

func TestConfirmWithinWithoutTimeIsUnavailable(t *testing.T) {
	for _, wait := range []time.Duration{0, -time.Second, 500 * time.Millisecond} {
		if got := ConfirmWithin("qualquer", wait); got != Unavailable {
			t.Errorf("espera %v: esperava Unavailable (sem janela), obtive %v", wait, got)
		}
	}
}

func TestDefaultWaitMatchesClaudeBudget(t *testing.T) {
	if DefaultWait != 480*time.Second {
		t.Errorf("DefaultWait = %v", DefaultWait)
	}
}
