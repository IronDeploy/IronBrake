package dialog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Caminho absoluto: um zenity falso no PATH aprovaria tudo sozinho.
//
// Só o zenity. O kdialog foi medido e não serve: o botão padrão dele é sempre
// o afirmativo (Enter aprova), e trocar os rótulos faria a aprovação depender
// do código 1, que também é o código de erro dele (opção inválida).
const zenityPath = "/usr/bin/zenity"

// O zenity cresce com o texto, e passando da altura da tela os botões ficam
// fora dela. O cartão do terraform tem até ~24 linhas; isto cobre.
const (
	maxLinuxLines = 24
	maxLinuxRunes = 1600
)

// zenity sai com estes códigos. Qualquer outro é falha do programa.
const (
	zenityOK      = 0 // Executar
	zenityCancel  = 1 // Cancelar, Enter, Esc, fechar a janela
	zenityTimeout = 5 // --timeout esgotado
)

// hasDisplay diz se há uma sessão gráfica (X11 ou Wayland). Em SSH e em CI não
// há, e o zenity nem abriria.
func hasDisplay(getenv func(string) string) bool {
	return getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
}

// zenityArgs monta os argumentos. A mensagem vai num único argumento colado a
// --text=, nunca interpretada por shell nem tomada como opção: nomes de
// recursos vêm do .tf, que o agente escreve. --no-markup impede que <b>, <span>
// e & do texto virem formatação. Cancelar é o botão padrão (Enter).
func zenityArgs(message string, wait time.Duration) []string {
	return []string{
		"--question",
		"--title=Iron Brake",
		"--text=" + limitMessage(message),
		"--no-markup",
		"--default-cancel",
		"--ok-label=Executar",
		"--cancel-label=Cancelar",
		"--icon-name=dialog-warning",
		"--width=560",
		"--timeout=" + strconv.Itoa(int(wait.Seconds())),
	}
}

// limitMessage corta o texto por linhas e por caracteres (nunca no meio de um)
// e marca o corte com ….
func limitMessage(message string) string {
	cut := false
	if lines := strings.Split(message, "\n"); len(lines) > maxLinuxLines {
		message, cut = strings.Join(lines[:maxLinuxLines], "\n"), true
	}
	if runes := []rune(message); len(runes) > maxLinuxRunes {
		message, cut = string(runes[:maxLinuxRunes]), true
	}
	if cut {
		return message + "…"
	}
	return message
}

// zenityAnswer traduz a saída do zenity. Só o código 0 aprova. Sem display ele
// sai com 1, o mesmo código do Cancelar, mas fala do display no stderr: isso é
// "não deu para perguntar", não uma recusa.
func zenityAnswer(code int, stderr string) Answer {
	switch code {
	case zenityOK:
		return Approved
	case zenityCancel:
		if strings.Contains(strings.ToLower(stderr), "display") {
			return Unavailable
		}
		return Rejected
	case zenityTimeout:
		return Rejected
	}
	return Unavailable
}

// confirmLinux abre o zenity. Sem display ou sem o programa, não tenta: a
// resposta é Unavailable. Desistir por tempo conta como recusa.
func confirmLinux(program, message string, wait time.Duration, getenv func(string) string) Answer {
	if !hasDisplay(getenv) {
		return Unavailable
	}
	if info, err := os.Stat(program); err != nil || !info.Mode().IsRegular() {
		return Unavailable
	}

	ctx, cancel := context.WithTimeout(context.Background(), wait+killMargin)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, program, zenityArgs(message, wait)...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return Rejected
	}

	var exit *exec.ExitError
	switch {
	case err == nil:
		return zenityAnswer(zenityOK, "")
	case errors.As(err, &exit):
		return zenityAnswer(exit.ExitCode(), stderr.String())
	}
	return Unavailable
}
