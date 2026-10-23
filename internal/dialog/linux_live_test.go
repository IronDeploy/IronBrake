package dialog

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// TestConfirmLinuxLive abre a janela de verdade. Só roda sob
// scripts/test-dialog-linux.sh (Docker com Xvfb), que define a resposta
// esperada e age sobre a janela com o xdotool. Com uma pessoa clicando, o tempo
// sobe com IRON_DIALOG_WAIT (segundos; padrão 6).
func TestConfirmLinuxLive(t *testing.T) {
	want := os.Getenv("IRON_DIALOG_EXPECT")
	if os.Getenv("IRON_DIALOG_LIVE") != "1" || want == "" {
		t.Skip("teste manual: use scripts/test-dialog-linux.sh")
	}

	message := os.Getenv("IRON_DIALOG_MESSAGE")
	if message == "" {
		message = "IRON BRAKE — terraform apply\nCriar: 0 | Alterar: 0 | Apagar: 1 | Substituir: 0"
	}
	wait := 6 * time.Second
	if v, err := strconv.Atoi(os.Getenv("IRON_DIALOG_WAIT")); err == nil && v > 0 {
		wait = time.Duration(v) * time.Second
	}
	got := ConfirmWithin(message, wait)
	if string(got) != want {
		t.Fatalf("esperava %q, obtive %q", want, got)
	}
}
