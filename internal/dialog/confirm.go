package dialog

import (
	"context"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Answer string

const (
	Approved    Answer = "approved"
	Rejected    Answer = "rejected"    // Cancelar, Esc ou tempo esgotado
	Unavailable Answer = "unavailable" // sem janela (fora do macOS, sem tela)
)

// A janela desiste antes de o agente liberar o comando (600 s no Claude Code);
// desistir conta como recusa. O padrão vale para o Claude Code; os outros
// agentes passam o próprio tempo em ConfirmWithin.
const (
	DefaultWait = 480 * time.Second

	// killMargin: o osascript tem esse tempo a mais para devolver a resposta
	// depois de desistir sozinho.
	killMargin = 20 * time.Second
)

// A mensagem entra por argv, nunca no código do script: nomes de recursos
// vêm do .tf, que o agente escreve. Cancelar lança o erro -128, que o try
// transforma em "Cancelar"; sem isso, a recusa viraria Unavailable.
var script = []string{
	"-e", "on run argv",
	"-e", "try",
	"-e", `set answer to display dialog (item 1 of argv) with title "Iron Brake" buttons {"Cancelar", "Executar"} default button "Cancelar" cancel button "Cancelar" with icon caution giving up after ((item 2 of argv) as integer)`,
	"-e", `if gave up of answer then return "timeout"`,
	"-e", "return button returned of answer",
	"-e", "on error number -128",
	"-e", `return "Cancelar"`,
	"-e", "end try",
	"-e", "end run",
}

// Caminho absoluto: um osascript falso no PATH aprovaria tudo sozinho.
const osascriptPath = "/usr/bin/osascript"

// Confirm mostra a mensagem numa janela do macOS e espera a resposta pelo
// tempo padrão.
func Confirm(message string) Answer { return ConfirmWithin(message, DefaultWait) }

// ConfirmWithin é o Confirm com o tempo de espera escolhido. Sem tempo
// (wait <= 0) não abre janela: a resposta é Unavailable.
func ConfirmWithin(message string, wait time.Duration) Answer {
	if runtime.GOOS != "darwin" || wait < time.Second {
		return Unavailable
	}

	ctx, cancel := context.WithTimeout(context.Background(), wait+killMargin)
	defer cancel()

	args := append(slices.Clone(script), message, strconv.Itoa(int(wait.Seconds())))
	output, err := exec.CommandContext(ctx, osascriptPath, args...).Output()
	if ctx.Err() != nil {
		return Rejected
	}

	return parseAnswer(string(output), err)
}

// parseAnswer: só a resposta exata "Executar" aprova.
func parseAnswer(output string, err error) Answer {
	if err != nil {
		return Unavailable
	}
	if strings.TrimSpace(output) == "Executar" {
		return Approved
	}
	return Rejected
}

// Notify mostra uma notificação do sistema (não uma janela de decisão: nada a
// responder). Fora do macOS não faz nada. O texto entra por argv, nunca no
// código do script.
func Notify(title, message string) {
	if runtime.GOOS != "darwin" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	args := []string{
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		title, message,
	}
	_ = exec.CommandContext(ctx, osascriptPath, args...).Run()
}
