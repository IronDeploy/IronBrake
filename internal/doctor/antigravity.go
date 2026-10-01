package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

// agyTestEvent é o stdin do PreToolUse do Antigravity com um force push.
const agyTestEvent = `{"toolCall":{"name":"run_command","args":{"CommandLine":"git push --force origin main","Cwd":"/"}},"conversationId":"doctor","workspacePaths":["/"]}`

const (
	nameAgyHooks   = "o hook do Iron Brake está no .agents/hooks.json"
	nameAgyTimeout = "o prazo gravado dá tempo à janela de confirmação do Iron Brake"
	nameAgyLimit   = "limites conhecidos do Antigravity (informativo)"
)

// RunAntigravity faz as verificações do Antigravity CLI numa pasta. Como em
// Run, se uma falha as que dependem dela também falham.
func RunAntigravity(dir, version string, timeout time.Duration, extra Extra) []Result {
	hooks, err := setup.FindAntigravity(dir)
	installed, usable := checkAgyHooks(dir, hooks, err)

	binary, responds := notChecked(nameBinary, 1), notChecked(nameResponds, 1)
	prazo := notChecked(nameAgyTimeout, 1)
	if installed.OK {
		chosen := pickAgyHook(usable)
		binary = checkBinary(chosen.Exe)
		if binary.OK {
			responds = checkRespondsWith(chosen.Exe, []string{setup.HookSubcommand, "--agent=antigravity"}, agyTestEvent, timeout, agyBlocked)
		} else {
			responds = notChecked(nameResponds, 2)
		}
		prazo = checkAgyTimeout(chosen)
	}

	results := []Result{
		installed, binary, responds, prazo,
		CheckTools(extra.ConfigPath, extra.ProjectDir), CheckAudit(extra.Log),
	}
	if extra.Coverage != nil {
		results = append(results, CheckCoverage(*extra.Coverage))
	}
	return append(results, agyLimits(), Result{Name: nameVersion, OK: true, Detail: version})
}

// agyBlocked: o Antigravity bloqueia com {"decision":"deny"} (exit 0) e com
// qualquer saída de erro; o hook do Iron Brake usa o JSON, e o 2 é o recuo
// quando não consegue escrevê-lo.
func agyBlocked(code int, stdout string) (bool, string) {
	var out struct {
		Decision string `json:"decision"`
	}
	switch {
	case code == 0 && json.Unmarshal([]byte(stdout), &out) == nil && out.Decision == "deny":
		return true, `o force push de teste foi bloqueado (decision "deny")`
	case code == 2:
		return true, "o force push de teste foi bloqueado (código 2)"
	}
	return false, ""
}

// checkAgyHooks: um arquivo que não é JSON válido faz o Antigravity ignorá-lo
// em silêncio, e o comando passa sem hook; por isso é falha, com o motivo.
func checkAgyHooks(dir string, hooks []setup.AntigravityHook, err error) (Result, []setup.AntigravityHook) {
	result := Result{Name: nameAgyHooks}
	path := setup.AntigravityHooksPath(dir)

	var usable []setup.AntigravityHook
	var off []string
	for _, h := range hooks {
		switch {
		case !h.Enabled:
			off = append(off, fmt.Sprintf("o conjunto %q está com enabled:false", h.Set))
		case !setup.MatcherCovers(h.Matcher, hook.AntigravityShellTool):
			off = append(off, fmt.Sprintf("o matcher %q do conjunto %q não pega a ferramenta %s", h.Matcher, h.Set, hook.AntigravityShellTool))
		default:
			usable = append(usable, h)
		}
	}

	switch {
	case errors.Is(err, fs.ErrNotExist):
		result.Detail = path + " não existe"
		result.Fix = "rode iron init --agent=antigravity na raiz do projeto."
	case err != nil:
		result.Detail = err.Error() + ": o Antigravity ignora um hooks.json inválido SEM avisar, e os comandos passam sem o Iron Brake"
		result.Fix = "corrija o arquivo (JSON estrito) e rode iron doctor --agent=antigravity de novo."
	case len(usable) > 0:
		result.OK = true
		result.Detail = fmt.Sprintf("conjunto %q em %s (matcher %q)", usable[0].Set, path, usable[0].Matcher)
	case len(off) > 0:
		result.Detail = strings.Join(off, "; ")
		result.Fix = "rode iron init --agent=antigravity para corrigir."
	default:
		result.Detail = "o arquivo existe, mas não tem o hook do Iron Brake"
		result.Fix = "rode iron init --agent=antigravity para adicionar o hook sem apagar o resto do arquivo."
	}
	return result, usable
}

func pickAgyHook(hooks []setup.AntigravityHook) setup.AntigravityHook {
	for _, h := range hooks {
		if checkBinary(h.Exe).OK {
			return h
		}
	}
	return hooks[0]
}

// checkAgyTimeout: no Antigravity um hook que estoura o prazo é morto e o
// comando é BLOQUEADO (não é falha aberta), mas o prazo padrão de 30 s é menor
// que a janela de confirmação (480 s): ela seria cortada e o comando bloqueado.
func checkAgyTimeout(h setup.AntigravityHook) Result {
	result := Result{Name: nameAgyTimeout}
	minimum := hook.Antigravity.Capabilities().Budget().Deadline.Seconds() + 10

	switch {
	case h.Timeout == 0:
		result.Detail = "sem o campo timeout: vale o padrão de 30 s, e o Antigravity mata o hook e bloqueia o comando antes de a janela de confirmação terminar"
	case h.Timeout < minimum:
		result.Detail = fmt.Sprintf("%g s (mínimo %g s): a janela de confirmação seria cortada e o comando, bloqueado", h.Timeout, minimum)
	default:
		result.OK = true
		result.Detail = fmt.Sprintf("%g s", h.Timeout)
		return result
	}
	result.Fix = fmt.Sprintf("rode iron init --agent=antigravity (timeout %d) ou ajuste na mão para pelo menos %g s.", int(hook.AntigravityTimeout.Seconds()), minimum)
	return result
}

// agyLimits só informa o que o doctor não consegue testar por fora.
func agyLimits() Result {
	return Result{Name: nameAgyLimit, OK: true, Detail: "o hook só roda em pasta confiada no modo interativo; " +
		`com --dangerously-skip-permissions o hook ainda bloqueia, mas "ask" não segura nada (o Iron Brake não o usa); ` +
		"um hooks.json inválido desliga o hook sem aviso; hook global em ~/.gemini/config/hooks.json junto com este rodaria o Iron Brake duas vezes"}
}
