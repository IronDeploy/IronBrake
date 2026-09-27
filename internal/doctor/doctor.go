package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

const testEvent = `{"tool_name":"Bash","tool_input":{"command":"git push --force origin main"}}`

const (
	nameSettings = "settings.json contém o hook do Iron Brake"
	nameBinary   = "o binário do hook existe e é executável"
	nameResponds = "o hook bloqueia um force push de teste"
	nameTimeout  = "o timeout do hook dá tempo ao Iron Brake"
	nameVersion  = "versão"
)

type Result struct {
	Name   string
	OK     bool
	Detail string
	Fix    string // preenchido só quando falha
}

// Run executa as 4 verificações em ordem. Se uma falha, as que dependem dela
// também falham: não dá para afirmar que protege sem ter verificado.
func Run(settingsPath, version string, timeout time.Duration) []Result {
	settings, hooks := checkSettings(settingsPath)
	binary, responds := notChecked(nameBinary, 1), notChecked(nameResponds, 1)
	hookTimeout := notChecked(nameTimeout, 1)

	if settings.OK {
		chosen := pickHook(hooks)
		binary = checkBinary(chosen.Command)
		hookTimeout = checkTimeout(chosen.Timeout)

		if binary.OK {
			responds = checkResponds(chosen.Command, timeout)
		} else {
			responds = notChecked(nameResponds, 2)
		}
	}

	return []Result{settings, binary, responds, hookTimeout, {Name: nameVersion, OK: true, Detail: version}}
}

func Print(w io.Writer, results []Result) {
	failed := 0

	for i, r := range results {
		status := "OK   "
		if !r.OK {
			status = "FALHA"
			failed++
		}

		fmt.Fprintf(w, "%s  %d/%d  %s\n", status, i+1, len(results), r.Name)
		if r.Detail != "" {
			fmt.Fprintf(w, "             %s\n", r.Detail)
		}
		if r.Fix != "" {
			fmt.Fprintf(w, "             Como corrigir: %s\n", r.Fix)
		}
	}

	if failed > 0 {
		fmt.Fprintf(w, "\n%d de %d verificações falharam.\n", failed, len(results))
	} else {
		fmt.Fprintln(w, "\nTudo certo: o hook está instalado e bloqueou o force push de teste.")
	}
}

func AllOK(results []Result) bool {
	return !slices.ContainsFunc(results, func(r Result) bool { return !r.OK })
}

func notChecked(name string, dependsOn int) Result {
	return Result{
		Name:   name,
		Detail: "não foi possível verificar",
		Fix:    fmt.Sprintf("corrija primeiro a verificação %d e rode iron doctor de novo.", dependsOn),
	}
}

func checkSettings(settingsPath string) (Result, []setup.Hook) {
	result := Result{Name: nameSettings}

	hooks, err := setup.FindHooks(settingsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		result.Detail = settingsPath + " não existe"
		result.Fix = "rode iron init na raiz do projeto."
	case err != nil:
		result.Detail = err.Error()
		result.Fix = "corrija o arquivo (JSON estrito: sem comentários nem vírgula sobrando) e rode iron doctor de novo."
	case len(hooks) == 0:
		result.Detail = "o arquivo existe, mas não tem o hook do Iron Brake"
		result.Fix = "rode iron init para adicionar o hook sem apagar o resto do arquivo."
	default:
		result.OK = true
		result.Detail = fmt.Sprintf("%d hook(s) em %s", len(hooks), settingsPath)
	}

	return result, hooks
}

// pickHook prefere o primeiro hook cujo binário funciona.
func pickHook(hooks []setup.Hook) setup.Hook {
	for _, h := range hooks {
		if checkBinary(h.Command).OK {
			return h
		}
	}
	return hooks[0]
}

// checkTimeout: um timeout menor que hook.MinTimeout faz o Claude Code
// desistir antes do deny por prazo do Iron Brake, e o comando passa.
func checkTimeout(seconds float64) Result {
	result := Result{Name: nameTimeout}
	minimum := hook.MinTimeout.Seconds()

	switch {
	case seconds == 0:
		result.OK = true
		result.Detail = "padrão do Claude Code (600 s)"
	case seconds >= minimum:
		result.OK = true
		result.Detail = fmt.Sprintf("%g s", seconds)
	default:
		result.Detail = fmt.Sprintf("%g s: o Claude Code desiste antes do prazo do Iron Brake (%g s) e deixa o comando passar", seconds, hook.Deadline.Seconds())
		result.Fix = fmt.Sprintf("remova o campo \"timeout\" do hook no settings.json (o padrão é 600 s) ou use pelo menos %g.", minimum)
	}
	return result
}

func checkBinary(command string) Result {
	result := Result{Name: nameBinary}

	// Caminho relativo seria resolvido dentro do projeto, que pode ser de
	// terceiros: só executamos caminho absoluto ou procurado no PATH.
	if strings.ContainsRune(command, filepath.Separator) && !filepath.IsAbs(command) {
		result.Detail = fmt.Sprintf("o caminho %s é relativo", command)
		result.Fix = "rode iron init de novo para gravar o caminho absoluto."
		return result
	}

	path, err := exec.LookPath(command)
	if err != nil {
		result.Detail = fmt.Sprintf("o binário %s não existe ou não tem permissão de execução", command)
		result.Fix = "recompile com go build -o <destino>/iron ./cmd/iron e rode iron init de novo."
		return result
	}

	result.OK = true
	result.Detail = path
	return result
}

// checkResponds roda o hook como o Claude Code faria, sem shell, e exige o
// código 2 para um force push.
func checkResponds(command string, timeout time.Duration) Result {
	result := Result{Name: nameResponds}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, setup.HookSubcommand)
	cmd.Stdin = strings.NewReader(testEvent)
	cmd.WaitDelay = time.Second
	err := cmd.Run()

	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		result.Detail = fmt.Sprintf("o hook não respondeu em %s", timeout)
		result.Fix = "rode " + command + " hook e veja por que trava; o Claude Code deixa passar o comando de um hook que estoura o tempo."
	case err == nil:
		result.Detail = "o hook liberou o force push de teste (saiu com código 0)"
		result.Fix = "recompile o binário (go build) e rode iron init de novo; este hook NÃO está protegendo."
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 2:
		result.OK = true
		result.Detail = "o force push de teste foi bloqueado (código 2)"
	case errors.As(err, &exitErr):
		result.Detail = fmt.Sprintf("o hook saiu com código %d em vez de 2", exitErr.ExitCode())
		result.Fix = "só o código 2 bloqueia; recompile o binário (go build) e rode iron init de novo."
	default:
		result.Detail = "não consegui executar o hook: " + err.Error()
		result.Fix = "confira as permissões do arquivo e rode iron doctor de novo."
	}

	return result
}
