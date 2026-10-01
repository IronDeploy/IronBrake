package doctor

import (
	"fmt"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

// kiroTestEvent é o stdin do preToolUse do Kiro v2 (shell) com um force push.
const kiroTestEvent = `{"hook_event_name":"preToolUse","cwd":"/","session_id":"doctor","tool_name":"shell","tool_input":{"command":"git push --force origin main"}}`

const (
	nameKiroHooks   = "o hook do Iron Brake está nos dois formatos do Kiro (agente v2 e .kiro/hooks do v3)"
	nameKiroTimeout = "os prazos gravados dão tempo ao Iron Brake"
	nameKiroLimit   = "limites conhecidos do Kiro (informativo)"
)

// kiroMinTimeout é o menor prazo aceitável: o deny por Deadline precisa chegar
// antes de o Kiro desistir (e liberar o comando).
func kiroMinTimeout() time.Duration {
	return hook.Kiro.Capabilities().Budget().Deadline + 10*time.Second
}

// RunKiro faz as verificações do Kiro CLI numa pasta. Como em Run, se uma
// falha as que dependem dela também falham.
func RunKiro(dir, version string, timeout time.Duration, extra Extra) []Result {
	hooks, err := setup.FindKiro(dir)
	installed := checkKiroHooks(hooks, err)

	binary, responds := notChecked(nameBinary, 1), notChecked(nameResponds, 1)
	prazo := notChecked(nameKiroTimeout, 1)
	if installed.OK {
		chosen := pickKiroExe(hooks)
		binary = checkBinary(chosen)
		if binary.OK {
			responds = checkResponds(chosen, []string{setup.HookSubcommand, "--agent=kiro"}, kiroTestEvent, timeout)
		} else {
			responds = notChecked(nameResponds, 2)
		}
		prazo = checkKiroTimeouts(hooks)
	}

	return []Result{
		installed, binary, responds, prazo,
		CheckTools(extra.ConfigPath, extra.ProjectDir), CheckAudit(extra.Log),
		kiroLimits(hooks),
		{Name: nameVersion, OK: true, Detail: version},
	}
}

func checkKiroHooks(h setup.KiroHooks, err error) Result {
	result := Result{Name: nameKiroHooks}
	switch {
	case err != nil:
		result.Detail = err.Error()
		result.Fix = "corrija o arquivo (JSON estrito) e rode iron doctor --agent=kiro de novo."
	case len(h.V2) == 0 && len(h.V3) == 0:
		result.Detail = "nenhum hook do Iron Brake em .kiro/agents nem em .kiro/hooks"
		result.Fix = "rode iron init --agent=kiro na raiz do projeto."
	case len(h.V2) == 0:
		result.Detail = "falta o hook no agente do v2: o Kiro v2 só lê hook de dentro do agente e esta pasta fica sem proteção nele"
		result.Fix = "rode iron init --agent=kiro."
	case len(h.V3) == 0:
		result.Detail = "falta o .kiro/hooks/iron-brake.json: o Kiro v3 não lê o hook que está no agente v2 e esta pasta fica sem proteção nele"
		result.Fix = "rode iron init --agent=kiro."
	default:
		result.OK = true
		result.Detail = fmt.Sprintf("v2: %s; v3: %s", kiroFiles(h.V2), kiroFiles(h.V3))
	}
	return result
}

func kiroFiles(hooks []setup.KiroHook) string {
	files := make([]string, len(hooks))
	for i, h := range hooks {
		files[i] = h.File
	}
	return strings.Join(files, ", ")
}

// pickKiroExe prefere o primeiro programa que funciona.
func pickKiroExe(h setup.KiroHooks) string {
	all := append(append([]setup.KiroHook{}, h.V2...), h.V3...)
	for _, k := range all {
		if checkBinary(k.Exe).OK {
			return k.Exe
		}
	}
	return all[0].Exe
}

// checkKiroTimeouts exige o prazo escrito nos dois formatos: sem o campo, o v2
// libera o comando depois de ~10 s (medido com o Kiro 2.26.1), muito antes da
// janela de confirmação.
func checkKiroTimeouts(h setup.KiroHooks) Result {
	result := Result{Name: nameKiroTimeout}
	minimum := kiroMinTimeout().Seconds()

	var bad []string
	for _, k := range append(append([]setup.KiroHook{}, h.V2...), h.V3...) {
		switch {
		case k.Timeout == 0:
			bad = append(bad, fmt.Sprintf("%s sem prazo (o Kiro libera o comando se o hook demorar)", k.File))
		case k.Timeout < minimum:
			bad = append(bad, fmt.Sprintf("%s com %g s (mínimo %g s)", k.File, k.Timeout, minimum))
		}
	}
	if len(bad) > 0 {
		result.Detail = strings.Join(bad, "; ")
		result.Fix = fmt.Sprintf("rode iron init --agent=kiro (timeout_ms %d no agente v2 e timeout %d no .kiro/hooks) ou ajuste na mão para pelo menos %g s.", hook.KiroTimeout.Milliseconds(), int(hook.KiroTimeout.Seconds()), minimum)
		return result
	}
	result.OK = true
	result.Detail = fmt.Sprintf("pelo menos %g s em todos os arquivos", minimum)
	return result
}

// kiroLimits só informa: são limites do Kiro que o doctor não consegue
// testar por fora.
func kiroLimits(h setup.KiroHooks) Result {
	r := Result{Name: nameKiroLimit, OK: true}
	r.Detail = "o Kiro v3 em modo não interativo (--no-interactive) não executa hooks: ali o Iron Brake não protege (issue kirodotdev/Kiro#11281); o Kiro IDE não envia o comando ao hook"
	if len(h.Doubled) > 0 {
		r.Detail += "; " + strings.Join(h.Doubled, ", ") + " (formato v3) também tem o hook: no v3 ele roda duas vezes por comando"
	}
	return r
}
