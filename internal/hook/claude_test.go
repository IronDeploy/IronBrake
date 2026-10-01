package hook

import (
	"bytes"
	"errors"
	"testing"
)

func TestRespond(t *testing.T) {
	cases := []struct {
		name       string
		decision   Decision
		reason     string
		wantStdout string
		wantStderr string
		wantCode   int
	}{
		// allow: sem saída nenhuma; o motivo é ignorado.
		{"allow", Allow, "irrelevante", "", "", 0},
		// ask: JSON oficial no stdout (com a quebra de linha do final) e saída 0.
		{
			"ask", Ask, "confirme antes de aplicar",
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"confirme antes de aplicar"},"systemMessage":"confirme antes de aplicar"}` + "\n",
			"", 0,
		},
		// userApproved: JSON "allow" explícito, para o Claude Code não perguntar de novo.
		{
			"userApproved", UserApproved, "aprovado na janela",
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"aprovado na janela"}}` + "\n",
			"", 0,
		},
		// deny: nada no stdout, o motivo vai para o stderr e a saída é 2.
		{"deny", Deny, "git push --force bloqueado", "", "git push --force bloqueado\n", 2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := Claude.Respond(&stdout, &stderr, c.decision, c.reason)

			if code != c.wantCode {
				t.Errorf("código de saída: esperava %d, obtive %d", c.wantCode, code)
			}
			if stdout.String() != c.wantStdout {
				t.Errorf("stdout: esperava %q, obtive %q", c.wantStdout, stdout.String())
			}
			if stderr.String() != c.wantStderr {
				t.Errorf("stderr: esperava %q, obtive %q", c.wantStderr, stderr.String())
			}
		})
	}
}

func TestRespondUnknownDecisionBlocks(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Claude.Respond(&stdout, &stderr, Decision("maybe"), "")

	if code != 2 {
		t.Errorf("código de saída: esperava 2, obtive %d", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout deveria estar vazio, obtive %q", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Error("stderr deveria explicar o motivo do bloqueio")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("falha simulada")
}

func TestRespondAskWriteFailureBlocks(t *testing.T) {
	var stderr bytes.Buffer

	code := Claude.Respond(failingWriter{}, &stderr, Ask, "confirme")

	if code != 2 {
		t.Errorf("código de saída: esperava 2, obtive %d", code)
	}
	if stderr.Len() == 0 {
		t.Error("stderr deveria explicar a falha")
	}
}
