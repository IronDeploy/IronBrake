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

// ironMatcher cobre as duas ferramentas de shell do Claude Code. Sem o Git for
// Windows o agente só tem a PowerShell; com ele, tem as duas e o que o Bash
// barra passaria pela PowerShell.
const ironMatcher = "Bash|PowerShell"

// legacyMatcher é o que as versões anteriores instalavam. O init o amplia no
// lugar; o doctor avisa que ele não cobre a PowerShell.
const legacyMatcher = "Bash"

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

	groups := doc.groups
	if i := legacyGroup(groups, ours); i >= 0 {
		// Instalado por uma versão sem a PowerShell: amplia o matcher no lugar.
		widened, err := widenMatcher(groups[i])
		if err != nil {
			return false, err
		}
		groups = slices.Clone(groups)
		groups[i] = widened
	} else {
		group, err := encode(matcherGroup{Matcher: ironMatcher, Hooks: []handler{ours}}, "")
		if err != nil {
			return false, err
		}
		groups = append(slices.Clone(groups), group)
	}
	if doc.hooks["PreToolUse"], err = encode(groups, ""); err != nil {
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
	if err := writeFileAtomic(settingsPath, append(out, '\n')); err != nil {
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
	return findGroup(groups, ours, ironMatcher) >= 0
}

// legacyGroup devolve o índice do nosso grupo com o matcher antigo, ou -1.
func legacyGroup(groups []json.RawMessage, ours handler) int {
	return findGroup(groups, ours, legacyMatcher)
}

// findGroup devolve o índice do primeiro grupo com esse matcher que tem o nosso
// handler, ou -1.
func findGroup(groups []json.RawMessage, ours handler, matcher string) int {
	return slices.IndexFunc(groups, func(raw json.RawMessage) bool {
		var group matcherGroup
		if json.Unmarshal(raw, &group) != nil || group.Matcher != matcher {
			return false
		}

		return slices.ContainsFunc(group.Hooks, func(h handler) bool {
			// O caminho é comparado limpo: "x/../bin/iron" e "bin/iron" são o mesmo programa.
			return h.Type == ours.Type && filepath.Clean(h.Command) == filepath.Clean(ours.Command) && slices.Equal(h.Args, ours.Args)
		})
	})
}

// widenMatcher troca só o matcher do grupo; o resto (timeout e campos que
// este código não conhece) fica como estava.
func widenMatcher(raw json.RawMessage) (json.RawMessage, error) {
	fields, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	if fields["matcher"], err = encode(ironMatcher, ""); err != nil {
		return nil, err
	}

	return encode(fields, "")
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

// SetEnv garante settings.env[key] = value. O resto do conteúdo fica, mas as
// chaves do arquivo são reordenadas e reindentadas (o mesmo que o InstallHook faz).
// Se a chave já existe com outro valor, não mexe: devolve o valor atual em
// current (a pessoa escolheu aquele valor).
func SetEnv(settingsPath, key, value string) (changed bool, current string, err error) {
	settings := map[string]json.RawMessage{}
	switch data, readErr := os.ReadFile(settingsPath); {
	case errors.Is(readErr, fs.ErrNotExist):
	case readErr != nil:
		return false, "", readErr
	default:
		if settings, err = decodeObject(data); err != nil {
			return false, "", fmt.Errorf("%s: %w", settingsPath, err)
		}
	}

	env, err := decodeObject(settings["env"])
	if err != nil {
		return false, "", fmt.Errorf(`%s: a chave "env" %w`, settingsPath, err)
	}

	if raw, ok := env[key]; ok {
		var existing string
		if json.Unmarshal(raw, &existing) != nil {
			existing = string(raw)
		}
		return false, existing, nil
	}

	if env[key], err = encode(value, ""); err != nil {
		return false, "", err
	}
	if settings["env"], err = encode(env, ""); err != nil {
		return false, "", err
	}
	out, err := encode(settings, "  ")
	if err != nil {
		return false, "", err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return false, "", err
	}
	if err := writeFileAtomic(settingsPath, append(out, '\n')); err != nil {
		return false, "", err
	}
	return true, value, nil
}

// writeFileAtomic grava num temporário ao lado e renomeia: uma falha no meio
// nunca deixa o settings.json truncado (o que desligaria o hook). Mantém a
// permissão do arquivo que já existia; um arquivo novo sai com 0644.
func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // some depois do rename; limpa se algo falhar

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
