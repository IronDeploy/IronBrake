package setup

import (
	"encoding/json"
	"path/filepath"
	"slices"
)

// ironBinaryName: só handlers que apontam para um binário com esse nome contam,
// para o doctor nunca executar um programa listado por um settings.json de
// terceiros.
const ironBinaryName = "iron"

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
		filepath.Base(h.Command) == ironBinaryName &&
		slices.Equal(h.Args, []string{HookSubcommand})
}
