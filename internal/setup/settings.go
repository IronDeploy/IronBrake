package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

const ironMatcher = "Bash"

// HookSubcommand é o argumento de "iron hook".
const HookSubcommand = "hook"

// handler e matcherGroup servem só para criar e achar o nosso hook; o resto
// do arquivo é mantido como JSON cru.
type handler struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout float64  `json:"timeout,omitempty"`
}

type matcherGroup struct {
	Matcher string    `json:"matcher"`
	Hooks   []handler `json:"hooks"`
}

var errNotObject = errors.New("não é um objeto JSON válido (comentários e vírgula sobrando também são erro)")

// InstallHook garante o hook em settingsPath sem alterar o resto do arquivo.
// Devolve false se ele já estava lá.
func InstallHook(settingsPath, exePath string) (bool, error) {
	doc, err := load(settingsPath)
	if errors.Is(err, fs.ErrNotExist) {
		doc = document{settings: map[string]json.RawMessage{}, hooks: map[string]json.RawMessage{}}
	} else if err != nil {
		return false, err
	}

	// Formato exec (command + args): sem shell, o caminho não precisa de escape.
	ours := handler{Type: "command", Command: exePath, Args: []string{HookSubcommand}}
	if isInstalled(doc.groups, ours) {
		return false, nil
	}

	group, err := encode(matcherGroup{Matcher: ironMatcher, Hooks: []handler{ours}}, "")
	if err != nil {
		return false, err
	}
	if doc.hooks["PreToolUse"], err = encode(append(doc.groups, group), ""); err != nil {
		return false, err
	}
	if doc.settings["hooks"], err = encode(doc.hooks, ""); err != nil {
		return false, err
	}
	out, err := encode(doc.settings, "  ")
	if err != nil {
		return false, err
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		return false, err
	}

	return true, nil
}

type document struct {
	settings map[string]json.RawMessage
	hooks    map[string]json.RawMessage
	groups   []json.RawMessage
}

func load(settingsPath string) (document, error) {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return document{}, err
	}

	settings, err := decodeObject(data)
	if err != nil {
		return document{}, fmt.Errorf("%s: %w", settingsPath, err)
	}

	hooks, err := decodeObject(settings["hooks"])
	if err != nil {
		return document{}, fmt.Errorf(`%s: a chave "hooks" %w`, settingsPath, err)
	}

	var groups []json.RawMessage
	if raw := hooks["PreToolUse"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return document{}, fmt.Errorf(`%s: a chave "hooks.PreToolUse" não é uma lista`, settingsPath)
		}
	}

	return document{settings: settings, hooks: hooks, groups: groups}, nil
}

// decodeObject: vazio vira objeto vazio; erros nunca repetem o conteúdo.
func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	object := map[string]json.RawMessage{}
	if len(raw) == 0 {
		return object, nil
	}

	// "null" é válido, mas deixaria o mapa nil.
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errNotObject
	}

	return object, nil
}

func isInstalled(groups []json.RawMessage, ours handler) bool {
	for _, raw := range groups {
		var group matcherGroup
		if json.Unmarshal(raw, &group) != nil || group.Matcher != ironMatcher {
			continue
		}

		found := slices.ContainsFunc(group.Hooks, func(h handler) bool {
			return h.Type == ours.Type && h.Command == ours.Command && slices.Equal(h.Args, ours.Args)
		})
		if found {
			return true
		}
	}

	return false
}

// encode não escapa <, > e & (o padrão do json.Marshal), para não reescrever
// os comandos que já estavam no arquivo.
func encode(value any, indent string) ([]byte, error) {
	var buf bytes.Buffer

	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indent)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
