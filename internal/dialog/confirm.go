package dialog

import (
	"context"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
)

type Answer string

const (
	Approved    Answer = "approved"
	Rejected    Answer = "rejected"    // Cancelar, Esc ou tempo esgotado
	Unavailable Answer = "unavailable" // sem janela (fora do macOS, sem tela)
)

// A janela desiste bem antes dos 600 s após os quais o Claude Code libera o
// comando; desistir conta como recusa.
const (
	giveUpSeconds = "480"
	dialogTimeout = 500 * time.Second
)

// A mensagem entra por argv, nunca no código do script: nomes de recursos
// vêm do .tf, que o agente escreve. Cancelar lança o erro -128, que o try
// transforma em "Cancelar"; sem isso, a recusa viraria Unavailable.
var script = []string{
	"-e", "on run argv",
	"-e", "try",
	"-e", `set answer to display dialog (item 1 of argv) with title "Iron Brake" buttons {"Cancelar", "Executar"} default button "Cancelar" cancel button "Cancelar" with icon caution giving up after ` + giveUpSeconds,
	"-e", `if gave up of answer then return "timeout"`,
	"-e", "return button returned of answer",
	"-e", "on error number -128",
	"-e", `return "Cancelar"`,
	"-e", "end try",
	"-e", "end run",
}

// Caminho absoluto: um osascript falso no PATH aprovaria tudo sozinho.
const osascriptPath = "/usr/bin/osascript"

// Confirm mostra a mensagem numa janela do macOS e espera a resposta.
func Confirm(message string) Answer {
	if runtime.GOOS != "darwin" {
		return Unavailable
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialogTimeout)
	defer cancel()

	args := append(slices.Clone(script), message)
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
