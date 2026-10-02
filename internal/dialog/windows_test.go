package dialog

import (
	"encoding/base64"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// commandOf devolve o texto do -Command: a partir dele o PowerShell executa.
func commandOf(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "-Command" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("sem -Command em %v", args)
	return ""
}

func TestPowershellArgsKeepMessageOutOfTheScript(t *testing.T) {
	// Nomes de recursos vêm do .tf, que o agente escreve: tudo isto precisa
	// chegar como dado, nunca como código do PowerShell.
	hostile := []string{
		`'; Remove-Item -Recurse C:\ ; '`,
		"$(Start-Process calc)",
		"`$(whoami)",
		`"; calc; "`,
		"linha 1\nlinha 2\r\nlinha 3",
		"aws_s3_bucket.prod[\"x'y\"]",
		"acentuação: ção, ñ, 日本語, emoji 🔥",
	}
	for _, message := range hostile {
		command := commandOf(t, powershellArgs(message, 480*time.Second))

		if !strings.HasPrefix(command, formScript+" '") {
			t.Errorf("o script fixo deveria vir primeiro e intacto (mensagem %q)", message)
		}
		// Depois do script só existem o argumento em base64 e os segundos.
		tail := strings.TrimPrefix(command, formScript)
		if !regexp.MustCompile(`^ '[A-Za-z0-9+/=]*' 480$`).MatchString(tail) {
			t.Errorf("depois do script deveria haver só base64 e segundos, obtive %q", tail)
		}
		// O que chegou é exatamente o que foi pedido.
		encoded := strings.Split(tail, "'")[1]
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || string(decoded) != message {
			t.Errorf("a mensagem não sobreviveu ao transporte: %q -> %q (%v)", message, decoded, err)
		}
	}
}

func TestFormScriptHasNoDoubleQuotesOrBackticks(t *testing.T) {
	// O texto vai numa linha de comando do Windows e o PowerShell usa ` como
	// escape: sem " nem `, não há o que escapar errado.
	if strings.ContainsAny(formScript, "\"`") {
		t.Error("o script não deve ter aspas duplas nem crase")
	}
}

func TestFormScriptCancelIsTheSafeDefault(t *testing.T) {
	for _, want := range []string{
		"$f.AcceptButton = $cancel", // Enter cancela
		"$f.CancelButton = $cancel", // Esc e o X cancelam
		"$res = @{ v = 'Cancelar' }",
		"exit 3", // sem desktop interativo: não espera um clique que ninguém vê
		"UserInteractive",
	} {
		if !strings.Contains(formScript, want) {
			t.Errorf("faltou %q no script", want)
		}
	}
	if strings.Contains(formScript, "$res = @{ v = 'Executar'") {
		t.Error("o resultado inicial nunca pode ser Executar")
	}
}

func TestPowershellArgsPassTheWaitInSeconds(t *testing.T) {
	for _, wait := range []time.Duration{time.Second, 45 * time.Second, DefaultWait} {
		command := commandOf(t, powershellArgs("x", wait))
		if !strings.HasSuffix(command, "' "+strconv.Itoa(int(wait.Seconds()))) {
			t.Errorf("espera %v: o script deveria terminar com os segundos, obtive %q", wait, command[len(command)-12:])
		}
	}
}

func TestPowershellArgsFlags(t *testing.T) {
	args := powershellArgs("x", time.Minute)
	for _, want := range []string{"-NoProfile", "-NonInteractive", "-Sta"} {
		found := false
		for _, a := range args {
			found = found || a == want
		}
		if !found {
			t.Errorf("faltou %s em %v", want, args)
		}
	}
}

func TestTruncate(t *testing.T) {
	short := strings.Repeat("a", maxMessageRunes)
	if truncate(short) != short {
		t.Error("texto no limite não deve ser cortado")
	}
	long := strings.Repeat("ç", maxMessageRunes+500)
	got := truncate(long)
	if r := []rune(got); len(r) != maxMessageRunes+1 || r[len(r)-1] != '…' {
		t.Errorf("deveria cortar em %d caracteres e marcar com …: %d", maxMessageRunes, len([]rune(got)))
	}
	// Cortar por caracteres, nunca no meio de um (UTF-8 inválido quebraria o base64 do transporte).
	if !strings.HasPrefix(got, strings.Repeat("ç", maxMessageRunes)) {
		t.Error("corte no meio de um caractere")
	}
}

func TestPowershellPathIsTheSystemOne(t *testing.T) {
	path := powershellPath()
	if !filepath.IsAbs(path) && !strings.HasPrefix(path, `C:\`) {
		t.Errorf("o caminho deveria ser absoluto: %q", path)
	}
	if !strings.HasSuffix(strings.ToLower(path), `\windowspowershell\v1.0\powershell.exe`) {
		t.Errorf("deveria ser o Windows PowerShell da pasta do sistema: %q", path)
	}
}
