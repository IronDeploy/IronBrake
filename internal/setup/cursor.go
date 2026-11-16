package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// Cursor (app e cursor-agent): o hook de projeto fica em .cursor/hooks.json,
// {"version":1,"hooks":{"beforeShellExecution":[{command,failClosed,timeout}]}}
// (documentação oficial e testes com o cursor-agent 2026.11.13 e o app 3.30.14).
// O "failClosed": true é o que faz o prazo estourado, o exit 1 e a saída vazia
// BLOQUEAREM; sem ele o Cursor libera o comando.
const (
	cursorHooksFile = ".cursor/hooks.json"
	cursorHookArg   = "--agent=cursor"
)

// CursorHooksPath é o arquivo de hooks do projeto.
func CursorHooksPath(dir string) string { return filepath.Join(dir, cursorHooksFile) }

// cursorHandler é uma entrada do beforeShellExecution. Os campos que não são
// nossos (matcher, type...) ficam de fora: só tocamos na entrada do Iron Brake.
type cursorHandler struct {
	Command    string  `json:"command"`
	FailClosed bool    `json:"failClosed"`
	Timeout    float64 `json:"timeout,omitempty"`
}

func cursorOurHandler(exePath string) cursorHandler {
	return cursorHandler{
		Command:    ironCommand(exePath, cursorHookArg),
		FailClosed: true,
		Timeout:    hook.CursorTimeout.Seconds(),
	}
}

func isCursorOurs(raw json.RawMessage) bool {
	var h cursorHandler
	if json.Unmarshal(raw, &h) != nil {
		return false
	}
	_, ok := parseIronCommand(h.Command, cursorHookArg)
	return ok
}

// loadCursorHooks lê o hooks.json: o documento, o objeto "hooks" e a lista do
// beforeShellExecution.
func loadCursorHooks(path string) (doc, hooks map[string]json.RawMessage, entries []json.RawMessage, err error) {
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
	if raw := hooks[hook.CursorEvent]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, nil, nil, fmt.Errorf(`%s: a chave "hooks.%s" não é uma lista`, path, hook.CursorEvent)
		}
	}
	return doc, hooks, entries, nil
}

// InstallCursor garante o hook do Iron Brake em dir/.cursor/hooks.json sem mexer
// no resto do arquivo. Um hook nosso com outro caminho, prazo ou sem failClosed
// é substituído no lugar.
func InstallCursor(dir, exePath string) (InstallState, error) {
	path := CursorHooksPath(dir)
	doc, hooks, entries, err := loadCursorHooks(path)
	state := Installed
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc, hooks = map[string]json.RawMessage{}, map[string]json.RawMessage{}
		doc["version"] = json.RawMessage("1")
	case err != nil:
		return 0, err
	}
	// "version" tem de ser 1 (a documentação a pede).
	if string(doc["version"]) != "1" {
		doc["version"] = json.RawMessage("1")
		state = Updated
	}

	want, err := encode(cursorOurHandler(exePath), "")
	if err != nil {
		return 0, err
	}
	replaced := false
	for i, raw := range entries {
		if !isCursorOurs(raw) {
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

	if hooks[hook.CursorEvent], err = encode(entries, ""); err != nil {
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

// CursorHook é um hook do Iron Brake achado no hooks.json.
type CursorHook struct {
	File       string  // caminho do arquivo
	Exe        string  // programa, sem as aspas
	Timeout    float64 // segundos; 0 = o campo não existe (padrão do Cursor: 60 s)
	FailClosed bool    // sem isso, falha e prazo estourado LIBERAM o comando
}

// FindCursor lista os hooks do Iron Brake em dir/.cursor/hooks.json. Sem o
// arquivo, o erro satisfaz errors.Is(err, fs.ErrNotExist). JSON inválido é erro.
func FindCursor(dir string) ([]CursorHook, error) {
	path := CursorHooksPath(dir)
	_, _, entries, err := loadCursorHooks(path)
	if err != nil {
		return nil, err
	}
	var found []CursorHook
	for _, raw := range entries {
		var h cursorHandler
		if json.Unmarshal(raw, &h) != nil {
			continue
		}
		if exe, ok := parseIronCommand(h.Command, cursorHookArg); ok {
			found = append(found, CursorHook{File: path, Exe: exe, Timeout: h.Timeout, FailClosed: h.FailClosed})
		}
	}
	return found, nil
}
