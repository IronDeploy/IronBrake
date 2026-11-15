package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

// copilotTestEvent é o stdin do preToolUse do Copilot com um force push.
const copilotTestEvent = `{"sessionId":"doctor","timestamp":0,"cwd":"/","toolCallId":"doctor","toolName":"bash","toolArgs":{"command":"git push --force origin main"}}`

const (
	nameCopilotHooks   = "o hook do Iron Brake está em .github/hooks"
	nameCopilotTimeout = "o prazo gravado dá tempo ao Iron Brake"
	nameCopilotTrust   = "o Copilot confia na pasta (sem isso o hook NÃO roda no copilot -p)"
	nameCopilotLimit   = "limites conhecidos do Copilot (informativo)"
)

// RunCopilot faz as verificações do Copilot CLI numa pasta. configPath é o
// config.json do Copilot, onde ele guarda as pastas confiadas. Como em Run, se
// uma falha as que dependem dela também falham.
func RunCopilot(dir, configPath, version string, timeout time.Duration, extra Extra) []Result {
	hooks, err := setup.FindCopilot(dir)
	installed := checkCopilotHooks(dir, hooks, err)

	binary, responds := notChecked(nameBinary, 1), notChecked(nameResponds, 1)
	prazo, trust := notChecked(nameCopilotTimeout, 1), notChecked(nameCopilotTrust, 1)
	if installed.OK {
		chosen := pickCopilotHook(hooks)
		binary = checkBinary(chosen.Exe)
		if binary.OK {
			responds = checkRespondsWith(chosen.Exe, []string{setup.HookSubcommand, "--agent=copilot"}, copilotTestEvent, timeout, copilotBlocked)
		} else {
			responds = notChecked(nameResponds, 2)
		}
		prazo = checkCopilotTimeout(chosen)
		trust = checkCopilotTrust(dir, configPath)
	}

	results := []Result{
		installed, binary, responds, prazo, trust,
		CheckTools(extra.ConfigPath, extra.ProjectDir), CheckAudit(extra.Log),
	}
	if extra.Coverage != nil {
		results = append(results, CheckCoverage(*extra.Coverage))
	}
	return append(results, copilotLimits(), Result{Name: nameVersion, OK: true, Detail: version})
}

// copilotBlocked: o Copilot bloqueia com {"permissionDecision":"deny"} (exit 0) e
// com qualquer código diferente de 0; o hook do Iron Brake usa o JSON, e o 2 é o
// recuo quando não consegue escrevê-lo.
func copilotBlocked(code int, stdout string) (bool, string) {
	var out struct {
		Decision string `json:"permissionDecision"`
	}
	switch {
	case code == 0 && json.Unmarshal([]byte(stdout), &out) == nil && out.Decision == "deny":
		return true, `o force push de teste foi bloqueado (permissionDecision "deny")`
	case code == 2:
		return true, "o force push de teste foi bloqueado (código 2)"
	}
	return false, ""
}

func checkCopilotHooks(dir string, hooks []setup.CopilotHook, err error) Result {
	result := Result{Name: nameCopilotHooks}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		result.Detail = setup.CopilotHooksPath(dir) + " não existe"
		result.Fix = "rode iron init --agent=copilot na raiz do projeto."
		if root := setup.GitRoot(dir); root != "" && root != filepath.Clean(dir) {
			result.Detail += "; esta pasta é uma subpasta do repositório e o Copilot só lê .github/hooks da raiz"
			result.Fix = "rode iron doctor --agent=copilot e iron init --agent=copilot em " + root + "."
		}
	case err != nil:
		result.Detail = err.Error()
		result.Fix = "corrija o arquivo (JSON estrito) e rode iron doctor --agent=copilot de novo."
	case len(hooks) > 0:
		result.OK = true
		result.Detail = hooks[0].File
	default:
		result.Detail = "há arquivos em .github/hooks, mas nenhum tem o hook do Iron Brake"
		result.Fix = "rode iron init --agent=copilot para adicionar o hook sem apagar o resto."
	}
	return result
}

func pickCopilotHook(hooks []setup.CopilotHook) setup.CopilotHook {
	for _, h := range hooks {
		if checkBinary(h.Exe).OK {
			return h
		}
	}
	return hooks[0]
}

// checkCopilotTimeout: sem o campo vale o padrão do Copilot (30 s), que NÃO serve
// (a janela espera mais, e estourar o prazo libera o comando).
func checkCopilotTimeout(h setup.CopilotHook) Result {
	result := Result{Name: nameCopilotTimeout}
	minimum := hook.Copilot.Capabilities().Budget().Deadline.Seconds() + 10

	switch {
	case h.Timeout == 0:
		result.Detail = "sem timeoutSec o padrão do Copilot é 30 s: ele mata o hook e LIBERA o comando"
		result.Fix = fmt.Sprintf("rode iron init --agent=copilot (timeoutSec %d) ou ajuste na mão para pelo menos %g s.", int(hook.CopilotTimeout.Seconds()), minimum)
	case h.Timeout >= minimum:
		result.OK = true
		result.Detail = fmt.Sprintf("%g s", h.Timeout)
	default:
		result.Detail = fmt.Sprintf("%g s (mínimo %g s): o Copilot mata o hook que estoura o prazo e LIBERA o comando", h.Timeout, minimum)
		result.Fix = fmt.Sprintf("rode iron init --agent=copilot (timeoutSec %d) ou ajuste na mão para pelo menos %g s.", int(hook.CopilotTimeout.Seconds()), minimum)
	}
	return result
}

// checkCopilotTrust: o Copilot só carrega os hooks do repositório se a pasta for
// confiada. No modo interativo ele pergunta; no copilot -p pula os hooks com um
// aviso e o comando roda sem o Iron Brake.
func checkCopilotTrust(dir, configPath string) Result {
	result := Result{Name: nameCopilotTrust}
	trust, err := setup.ReadCopilotTrust(configPath, dir)
	switch {
	case err != nil:
		result.Detail = "não consegui ler " + configPath + ": " + err.Error()
		result.Fix = "confira o arquivo e rode iron doctor --agent=copilot de novo."
	case !trust.FolderTrusted:
		result.Detail = "a pasta não está em trustedFolders do " + configPath + ": no copilot -p os hooks do projeto são ignorados, e no interativo só valem se você confiar na sessão"
		result.Fix = `abra o copilot nesta pasta e escolha "Yes, and remember this folder for future sessions".`
	default:
		result.OK = true
		result.Detail = "pasta confiada para futuras sessões"
	}
	return result
}

// copilotLimits só informa o que o doctor não consegue testar por fora.
func copilotLimits() Result {
	return Result{Name: nameCopilotLimit, OK: true, Detail: "o Copilot LIBERA o comando se o hook estourar o prazo; " +
		"qualquer saída diferente de 0 bloqueia; hooks de repositório só valem em pasta confiada; " +
		"só as ferramentas bash e powershell são cobertas"}
}
