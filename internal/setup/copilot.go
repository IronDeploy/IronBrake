package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// GitHub Copilot CLI: o hook de projeto fica em .github/hooks/*.json,
// {"version":1,"hooks":{"preToolUse":[{type,bash,powershell,timeoutSec}]}}
// (documentação oficial e captura com o Copilot 1.0.112). O Iron Brake grava o
// arquivo próprio iron-brake.json e mantém o que já houver nele. O Copilot só
// carrega os hooks do repositório se a pasta for confiada (sessão ou
// "remember this folder"); sem isso ele os ignora com um aviso, inclusive no -p.
const (
	copilotHooksDir  = ".github/hooks"
	copilotHooksFile = "iron-brake.json"
	copilotHookArg   = "--agent=copilot"
)

// CopilotHooksPath é o arquivo de hooks que o Iron Brake grava.
func CopilotHooksPath(dir string) string {
	return filepath.Join(dir, copilotHooksDir, copilotHooksFile)
}

// copilotHandler é uma entrada do preToolUse. Os campos que não são nossos
// (cwd, env, comment...) ficam de fora: só tocamos na entrada do Iron Brake.
type copilotHandler struct {
	Type       string  `json:"type"`
	Bash       string  `json:"bash"`
	PowerShell string  `json:"powershell"`
	TimeoutSec float64 `json:"timeoutSec,omitempty"`
}

// powershellCommand chama o programa pelo operador & (aspas simples do
// PowerShell dobram a aspa interna).
func powershellCommand(exePath string) string {
	return "& '" + strings.ReplaceAll(exePath, "'", "''") + "' " + HookSubcommand + " " + copilotHookArg
}

func copilotOurHandler(exePath string) copilotHandler {
	return copilotHandler{
		Type:       "command",
		Bash:       ironCommand(exePath, copilotHookArg),
		PowerShell: powershellCommand(exePath),
		TimeoutSec: hook.CopilotTimeout.Seconds(),
	}
}

func isCopilotOurs(raw json.RawMessage) bool {
	var h copilotHandler
	if json.Unmarshal(raw, &h) != nil {
		return false
	}
	_, ok := parseIronCommand(h.Bash, copilotHookArg)
	return ok
}

// loadCopilotHooks lê um arquivo de hooks; devolve o documento, o objeto "hooks"
// e a lista do preToolUse.
func loadCopilotHooks(path string) (doc, hooks map[string]json.RawMessage, entries []json.RawMessage, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	if doc, err = decodeObject(data); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if hooks, err = decodeObject(doc["hooks"]); err != nil {
		return nil, nil, nil, fmt.Errorf(`%s: a chave "hooks" %w`, path, err)
	}
	if raw := hooks[hook.CopilotEvent]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, nil, nil, fmt.Errorf(`%s: a chave "hooks.%s" não é uma lista`, path, hook.CopilotEvent)
		}
	}
	return doc, hooks, entries, nil
}

// InstallCopilot garante o hook do Iron Brake em dir/.github/hooks/iron-brake.json
// sem mexer no resto do arquivo. Um hook nosso com outro caminho ou prazo é
// substituído no lugar.
func InstallCopilot(dir, exePath string) (InstallState, error) {
	path := CopilotHooksPath(dir)
	doc, hooks, entries, err := loadCopilotHooks(path)
	state := Installed
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc, hooks = map[string]json.RawMessage{}, map[string]json.RawMessage{}
		doc["version"] = json.RawMessage("1")
	case err != nil:
		return 0, err
	}
	// "version" tem de ser 1; um arquivo nosso sem ela não carrega no agente.
	if string(doc["version"]) != "1" {
		doc["version"] = json.RawMessage("1")
		state = Updated
	}

	want, err := encode(copilotOurHandler(exePath), "")
	if err != nil {
		return 0, err
	}
	replaced := false
	for i, raw := range entries {
		if !isCopilotOurs(raw) {
			continue
		}
		if sameJSON(raw, want) && state == Installed {
			return Already, nil
		}
		entries[i], replaced, state = want, true, Updated
		break
	}
	if !replaced {
		entries = append(entries, want)
	}

	if hooks[hook.CopilotEvent], err = encode(entries, ""); err != nil {
		return 0, err
	}
	if doc["hooks"], err = encode(hooks, ""); err != nil {
		return 0, err
	}
	out, err := encode(doc, "  ")
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	return state, writeFileAtomic(path, append(out, '\n'))
}

// CopilotHook é um hook do Iron Brake achado num arquivo de .github/hooks.
type CopilotHook struct {
	File    string  // caminho do arquivo
	Event   string  // "preToolUse" (camelCase)
	Exe     string  // programa, sem as aspas
	Timeout float64 // segundos; 0 = o campo não existe (padrão do Copilot: 30 s)
}

// FindCopilot lista os hooks do Iron Brake de dir/.github/hooks/*.json, de
// qualquer arquivo. Sem nenhum arquivo, o erro satisfaz errors.Is(err,
// fs.ErrNotExist). JSON inválido é erro: o Copilot não carrega o arquivo.
func FindCopilot(dir string) ([]CopilotHook, error) {
	files, err := filepath.Glob(filepath.Join(dir, copilotHooksDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fs.ErrNotExist
	}
	slices.Sort(files)

	var found []CopilotHook
	for _, file := range files {
		_, _, entries, err := loadCopilotHooks(file)
		if err != nil {
			return nil, err
		}
		for _, raw := range entries {
			var h copilotHandler
			if json.Unmarshal(raw, &h) != nil {
				continue
			}
			if exe, ok := parseIronCommand(h.Bash, copilotHookArg); ok {
				found = append(found, CopilotHook{File: file, Event: hook.CopilotEvent, Exe: exe, Timeout: h.TimeoutSec})
			}
		}
	}
	return found, nil
}

// GitRoot devolve a raiz do repositório que contém dir (a pasta com .git, pasta
// ou arquivo de worktree), ou "" se dir não está num repositório. O Copilot só
// lê .github/hooks dessa raiz: um hook numa subpasta não roda (verificado com o
// Copilot 1.0.112).
func GitRoot(dir string) string {
	for cur := filepath.Clean(dir); ; {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}
