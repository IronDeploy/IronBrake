package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

// codexTestEvent é o stdin do PreToolUse do Codex com um force push.
const codexTestEvent = `{"session_id":"doctor","turn_id":"doctor","cwd":"/","hook_event_name":"PreToolUse","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push --force origin main"},"tool_use_id":"doctor"}`

const (
	nameCodexHooks   = "o hook do Iron Brake está no .codex/hooks.json"
	nameCodexTimeout = "o prazo gravado dá tempo ao Iron Brake"
	nameCodexTrust   = "o Codex confia na pasta e no hook (sem isso o hook NÃO roda)"
	nameCodexLimit   = "limites conhecidos do Codex (informativo)"
)

// RunCodex faz as verificações do Codex CLI numa pasta. configPath é o
// ~/.codex/config.toml, onde o Codex guarda a confiança. Como em Run, se uma
// falha as que dependem dela também falham.
func RunCodex(dir, configPath, version string, timeout time.Duration, extra Extra) []Result {
	hooks, err := setup.FindCodex(dir)
	installed, usable := checkCodexHooks(dir, hooks, err)

	binary, responds := notChecked(nameBinary, 1), notChecked(nameResponds, 1)
	prazo, trust := notChecked(nameCodexTimeout, 1), notChecked(nameCodexTrust, 1)
	if installed.OK {
		chosen := pickCodexHook(usable)
		binary = checkBinary(chosen.Exe)
		if binary.OK {
			responds = checkResponds(chosen.Exe, []string{setup.HookSubcommand, "--agent=codex"}, codexTestEvent, timeout)
		} else {
			responds = notChecked(nameResponds, 2)
		}
		prazo = checkCodexTimeout(chosen)
		trust = checkCodexTrust(dir, configPath, usable)
	}

	results := []Result{
		installed, binary, responds, prazo, trust,
		CheckTools(extra.ConfigPath, extra.ProjectDir), CheckAudit(extra.Log),
	}
	if extra.Coverage != nil {
		results = append(results, CheckCoverage(*extra.Coverage))
	}
	return append(results, codexLimits(), Result{Name: nameVersion, OK: true, Detail: version})
}

func checkCodexHooks(dir string, hooks []setup.CodexHook, err error) (Result, []setup.CodexHook) {
	result := Result{Name: nameCodexHooks}
	path := setup.CodexHooksPath(dir)

	var usable []setup.CodexHook
	var off []string
	for _, h := range hooks {
		if setup.MatcherCovers(h.Matcher, hook.CodexShellTool) {
			usable = append(usable, h)
		} else {
			off = append(off, fmt.Sprintf("o matcher %q não pega a ferramenta %s", h.Matcher, hook.CodexShellTool))
		}
	}

	switch {
	case errors.Is(err, fs.ErrNotExist):
		result.Detail = path + " não existe"
		result.Fix = "rode iron init --agent=codex na raiz do projeto."
	case err != nil:
		result.Detail = err.Error()
		result.Fix = "corrija o arquivo (JSON estrito) e rode iron doctor --agent=codex de novo."
	case len(usable) > 0:
		result.OK = true
		result.Detail = fmt.Sprintf("%s (matcher %q)", path, usable[0].Matcher)
	case len(off) > 0:
		result.Detail = strings.Join(off, "; ")
		result.Fix = "rode iron init --agent=codex para corrigir."
	default:
		result.Detail = "o arquivo existe, mas não tem o hook do Iron Brake"
		result.Fix = "rode iron init --agent=codex para adicionar o hook sem apagar o resto do arquivo."
	}
	return result, usable
}

func pickCodexHook(hooks []setup.CodexHook) setup.CodexHook {
	for _, h := range hooks {
		if checkBinary(h.Exe).OK {
			return h
		}
	}
	return hooks[0]
}

// checkCodexTimeout: sem o campo vale o padrão do Codex (600 s), que serve; um
// prazo curto faria o Codex matar o hook e LIBERAR o comando.
func checkCodexTimeout(h setup.CodexHook) Result {
	result := Result{Name: nameCodexTimeout}
	minimum := hook.Codex.Capabilities().Budget().Deadline.Seconds() + 10

	switch {
	case h.Timeout == 0:
		result.OK = true
		result.Detail = "padrão do Codex (600 s)"
	case h.Timeout >= minimum:
		result.OK = true
		result.Detail = fmt.Sprintf("%g s", h.Timeout)
	default:
		result.Detail = fmt.Sprintf("%g s (mínimo %g s): o Codex mata o hook que estoura o prazo e LIBERA o comando", h.Timeout, minimum)
		result.Fix = fmt.Sprintf("rode iron init --agent=codex (timeout %d) ou ajuste na mão para pelo menos %g s; depois reveja o hook em /hooks.", int(hook.CodexTimeout.Seconds()), minimum)
	}
	return result
}

// checkCodexTrust: o Codex só roda hook de projeto depois de a pasta e o hook
// (por hash) estarem confiados, e no "codex exec" nada avisa. O hash não é
// recalculado aqui; um hooks.json mais novo que a última gravação do
// config.toml indica alteração depois da confiança.
func checkCodexTrust(dir, configPath string, hooks []setup.CodexHook) Result {
	result := Result{Name: nameCodexTrust}
	fix := "abra o codex nesta pasta, escolha confiar na pasta e revise o hook (Review hooks → t) ou use /hooks; depois abra uma sessão nova."

	trust, err := setup.ReadCodexTrust(configPath, dir)
	if err != nil {
		result.Detail = "não consegui ler " + configPath + ": " + err.Error()
		result.Fix = "confira o arquivo e rode iron doctor --agent=codex de novo."
		return result
	}

	var untrusted []string
	for _, h := range hooks {
		if !trust.TrustedHooks[h.Key] {
			untrusted = append(untrusted, h.Key)
		}
	}
	info, statErr := os.Stat(setup.CodexHooksPath(dir))

	switch {
	case !trust.ProjectTrusted:
		result.Detail = "a pasta não está confiada no " + configPath + ": o Codex desativa os hooks do projeto, sem aviso no codex exec"
		result.Fix = fix
	case len(untrusted) > 0:
		result.Detail = "o hook ainda não foi confiado (sem entrada em hooks.state do " + configPath + "): o Codex não o roda"
		result.Fix = fix
	case statErr == nil && !trust.ConfigModTime.IsZero() && info.ModTime().After(trust.ConfigModTime):
		result.Detail = "o hooks.json foi alterado depois da última gravação do " + configPath + ": se o hook mudou, o Codex exige confiar de novo e não o roda até lá"
		result.Fix = fix
	default:
		result.OK = true
		result.Detail = "pasta e hook confiados (o doctor não recalcula o hash: se você alterou o hook, o /hooks mostra 'modified')"
	}
	return result
}

// codexLimits só informa o que o doctor não consegue testar por fora.
func codexLimits() Result {
	return Result{Name: nameCodexLimit, OK: true, Detail: "o Codex LIBERA o comando se o hook falhar (exit 1, stdout inválido, ask ou allow em JSON, prazo estourado): " +
		"o Iron Brake só bloqueia com exit 2; o codex exec não avisa de hook não confiado; os hooks carregam no início da sessão " +
		"(depois de confiar, abra uma sessão nova; se o aviso de hooks desativados persistir no chat, rode codex app-server daemon restart); " +
		"no chat o hook roda no ambiente do daemon, não no do seu terminal; o codex exec abandona e refaz chamadas com hook acima de ~55 s " +
		"(a janela do Iron Brake espera no máximo 45 s); só a ferramenta Bash é coberta (apply_patch e MCP não são analisados)"}
}
