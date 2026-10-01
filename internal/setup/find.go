package setup

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// ironBinaryName: só handlers que apontam para um binário com esse nome (com
// ou sem ".exe") contam, para o doctor nunca executar um programa listado por
// um settings.json de terceiros.
const ironBinaryName = "iron"

// IsIronBinaryName diz se o nome do arquivo é o que o doctor reconhece: "iron"
// ou "iron.exe", sem diferenciar maiúsculas (o Windows não diferencia).
func IsIronBinaryName(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.TrimSuffix(name, ".exe") == ironBinaryName
}

func SettingsPath(dir string) string {
	return filepath.Join(dir, ".claude", "settings.json")
}

// Hook é um hook do Iron Brake no settings.json. Timeout 0 = padrão do
// Claude Code.
type Hook struct {
	Command string
	Timeout float64 // segundos
}

// FindHooks devolve os hooks do Iron Brake em settingsPath. Sem arquivo, o
// erro satisfaz errors.Is(err, fs.ErrNotExist).
func FindHooks(settingsPath string) ([]Hook, error) {
	doc, err := load(settingsPath)
	if err != nil {
		return nil, err
	}

	var hooks []Hook
	for _, raw := range doc.groups {
		var group matcherGroup
		if json.Unmarshal(raw, &group) != nil || group.Matcher != ironMatcher {
			continue
		}

		for _, h := range group.Hooks {
			if isIronHandler(h) {
				hooks = append(hooks, Hook{Command: h.Command, Timeout: h.Timeout})
			}
		}
	}

	return hooks, nil
}

func isIronHandler(h handler) bool {
	return h.Type == "command" &&
		IsIronBinaryName(h.Command) &&
		slices.Equal(h.Args, []string{HookSubcommand})
}

// FindRenamedHooks devolve os comandos que parecem o hook do Iron Brake (o
// argumento "hook" no matcher Bash) mas apontam para um programa que não se
// chama iron. O doctor não os reconhece nem os executa; só os lista, para dizer
// à pessoa por que o hook "não existe" depois de um "iron init" com o binário
// renomeado.
func FindRenamedHooks(settingsPath string) ([]string, error) {
	doc, err := load(settingsPath)
	if err != nil {
		return nil, err
	}

	var commands []string
	for _, raw := range doc.groups {
		var group matcherGroup
		if json.Unmarshal(raw, &group) != nil || group.Matcher != ironMatcher {
			continue
		}
		for _, h := range group.Hooks {
			if h.Type == "command" && slices.Equal(h.Args, []string{HookSubcommand}) && !IsIronBinaryName(h.Command) {
				commands = append(commands, h.Command)
			}
		}
	}
	return commands, nil
}
