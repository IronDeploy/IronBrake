package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

// cursorTestEvent é o stdin do beforeShellExecution do Cursor com um force push
// ("cwd" vem vazio, como no agente real).
const cursorTestEvent = `{"conversation_id":"doctor","generation_id":"doctor","model":"default","command":"git push --force origin main","cwd":"","sandbox":false,"hook_event_name":"beforeShellExecution","workspace_roots":["/"]}`

const (
	nameCursorHooks      = "o hook do Iron Brake está em .cursor/hooks.json"
	nameCursorTimeout    = "o prazo gravado dá tempo ao Iron Brake"
	nameCursorFailClosed = "o hook tem failClosed (sem isso, falha ou prazo estourado LIBERA o comando)"
	nameCursorLimit      = "limites conhecidos do Cursor (informativo)"
)

// RunCursor faz as verificações do Cursor (app e cursor-agent) numa pasta. Como
// em Run, se uma falha as que dependem dela também falham.
func RunCursor(dir, version string, timeout time.Duration, extra Extra) []Result {
	hooks, err := setup.FindCursor(dir)
	installed := checkCursorHooks(dir, hooks, err)

	binary, responds := notChecked(nameBinary, 1), notChecked(nameResponds, 1)
	prazo, failClosed := notChecked(nameCursorTimeout, 1), notChecked(nameCursorFailClosed, 1)
	if installed.OK {
		chosen := pickCursorHook(hooks)
		binary = checkBinary(chosen.Exe)
		if binary.OK {
			responds = checkRespondsWith(chosen.Exe, []string{setup.HookSubcommand, "--agent=cursor"}, cursorTestEvent, timeout, cursorBlocked)
		} else {
			responds = notChecked(nameResponds, 2)
		}
		prazo = checkCursorTimeout(chosen)
		failClosed = checkCursorFailClosed(chosen)
	}

	results := []Result{
		installed, binary, responds, prazo, failClosed,
		CheckTools(extra.ConfigPath, extra.ProjectDir), CheckAudit(extra.Log),
	}
	return append(results, cursorLimits(), Result{Name: nameVersion, OK: true, Detail: version})
}

// cursorBlocked: o Cursor bloqueia com {"permission":"deny"} (exit 0) e com exit
// 2; o hook do Iron Brake usa o JSON, e o 2 é o recuo quando não consegue escrevê-lo.
func cursorBlocked(code int, stdout string) (bool, string) {
	var out struct {
		Permission string `json:"permission"`
	}
	switch {
	case code == 0 && json.Unmarshal([]byte(stdout), &out) == nil && out.Permission == "deny":
		return true, `o force push de teste foi bloqueado (permission "deny")`
	case code == 2:
		return true, "o force push de teste foi bloqueado (código 2)"
	}
	return false, ""
}

func checkCursorHooks(dir string, hooks []setup.CursorHook, err error) Result {
	result := Result{Name: nameCursorHooks}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		result.Detail = setup.CursorHooksPath(dir) + " não existe"
		result.Fix = "rode iron init --agent=cursor na raiz do projeto."
	case err != nil:
		result.Detail = err.Error()
		result.Fix = "corrija o arquivo (JSON estrito) e rode iron doctor --agent=cursor de novo."
	case len(hooks) > 0:
		result.OK = true
		result.Detail = hooks[0].File
	default:
		result.Detail = setup.CursorHooksPath(dir) + " existe, mas não tem o hook do Iron Brake em beforeShellExecution"
		result.Fix = "rode iron init --agent=cursor para adicionar o hook sem apagar o resto."
	}
	return result
}

func pickCursorHook(hooks []setup.CursorHook) setup.CursorHook {
	for _, h := range hooks {
		if checkBinary(h.Exe).OK {
			return h
		}
	}
	return hooks[0]
}

// checkCursorTimeout: sem o campo vale o padrão do Cursor (60 s), que NÃO serve
// (a janela espera mais, e estourar o prazo libera o comando sem failClosed).
func checkCursorTimeout(h setup.CursorHook) Result {
	result := Result{Name: nameCursorTimeout}
	minimum := hook.Cursor.Capabilities().Budget().Deadline.Seconds() + 10
	fix := fmt.Sprintf("rode iron init --agent=cursor (timeout %d) ou ajuste na mão para pelo menos %g s.", int(hook.CursorTimeout.Seconds()), minimum)

	switch {
	case h.Timeout == 0:
		result.Detail = "sem timeout o padrão do Cursor é 60 s: ele corta o hook antes de a janela de confirmação terminar"
		result.Fix = fix
	case h.Timeout >= minimum:
		result.OK = true
		result.Detail = fmt.Sprintf("%g s", h.Timeout)
	default:
		result.Detail = fmt.Sprintf("%g s (mínimo %g s): o Cursor corta o hook antes de o Iron Brake responder", h.Timeout, minimum)
		result.Fix = fix
	}
	return result
}

// checkCursorFailClosed: sem failClosed, exit 1, saída vazia e prazo estourado
// deixam o comando executar (provado com o cursor-agent e com o app).
func checkCursorFailClosed(h setup.CursorHook) Result {
	result := Result{Name: nameCursorFailClosed}
	if h.FailClosed {
		result.OK = true
		result.Detail = "failClosed: true"
		return result
	}
	result.Detail = `sem "failClosed": true, se o hook falhar ou estourar o prazo o Cursor executa o comando`
	result.Fix = "rode iron init --agent=cursor, que grava failClosed: true."
	return result
}

// cursorLimits só informa o que o doctor não consegue testar por fora.
func cursorLimits() Result {
	return Result{Name: nameCursorLimit, OK: true, Detail: "só o shell é coberto (beforeShellExecution); " +
		"hooks de equipe, empresa e de usuário (~/.cursor/hooks.json) também rodam e o doctor não os vê; " +
		"o cursor-agent não roda em pasta não confiada, e a confiança do app não foi verificada; " +
		"o --force do cursor-agent aprova o ask nativo, por isso o Iron Brake nunca o usa"}
}
