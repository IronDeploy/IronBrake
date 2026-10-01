package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// Antigravity CLI (agy): o hook de projeto fica em .agents/hooks.json, um
// objeto de conjuntos nomeados (verificado com o agy 1.2.14). O Iron Brake é o
// conjunto "iron-brake"; o resto do arquivo é mantido. Só o arquivo do projeto
// é usado: um hook global (~/.gemini/config/hooks.json) junto com este faria o
// Iron Brake rodar duas vezes por comando.
const (
	antigravityHooksFile = ".agents/hooks.json"
	antigravitySetName   = "iron-brake"
	antigravityHookArg   = "--agent=antigravity"
)

// AntigravityHooksPath é o hooks.json de projeto.
func AntigravityHooksPath(dir string) string { return filepath.Join(dir, antigravityHooksFile) }

// InstallState diz o que o instalador fez com o hook.
type InstallState int

const (
	Installed InstallState = iota // criado
	Updated                       // já existia com outro caminho, matcher ou prazo; foi corrigido
	Already                       // já estava certo
)

type agyHandler struct {
	Type    string  `json:"type"`
	Command string  `json:"command"`
	Timeout float64 `json:"timeout,omitempty"`
}

type agyMatcherGroup struct {
	Matcher string       `json:"matcher"`
	Hooks   []agyHandler `json:"hooks"`
}

// agySet é um conjunto do hooks.json. Enabled é ponteiro: ausente vale true.
type agySet struct {
	Enabled    *bool             `json:"enabled,omitempty"`
	PreToolUse []agyMatcherGroup `json:"PreToolUse,omitempty"`
}

func agyOurSet(exePath string) agySet {
	return agySet{PreToolUse: []agyMatcherGroup{{
		Matcher: hook.AntigravityShellTool,
		Hooks: []agyHandler{{
			Type:    "command",
			Command: ironCommand(exePath, antigravityHookArg),
			Timeout: hook.AntigravityTimeout.Seconds(),
		}},
	}}}
}

// InstallAntigravity garante o conjunto iron-brake em dir/.agents/hooks.json.
func InstallAntigravity(dir, exePath string) (InstallState, error) {
	path := AntigravityHooksPath(dir)
	doc := map[string]json.RawMessage{}
	state := Installed
	switch data, err := os.ReadFile(path); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return 0, err
	default:
		if doc, err = decodeObject(data); err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
	}

	want, err := encode(agyOurSet(exePath), "")
	if err != nil {
		return 0, err
	}
	if raw, ok := doc[antigravitySetName]; ok {
		if sameJSON(raw, want) {
			return Already, nil
		}
		state = Updated
	}

	doc[antigravitySetName] = want
	out, err := encode(doc, "  ")
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	return state, writeFileAtomic(path, append(out, '\n'))
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && fmt.Sprint(x) == fmt.Sprint(y)
}

// AntigravityHook é o hook do Iron Brake achado no hooks.json.
type AntigravityHook struct {
	Set     string // nome do conjunto
	Exe     string // programa, sem as aspas
	Matcher string
	Timeout float64 // segundos; 0 = o campo não existe (padrão do agy: 30 s)
	Enabled bool
}

// FindAntigravity lista os hooks do Iron Brake de dir/.agents/hooks.json. Sem
// arquivo, o erro satisfaz errors.Is(err, fs.ErrNotExist). JSON inválido é
// erro: o agy ignora o arquivo em silêncio e o comando passa sem hook.
func FindAntigravity(dir string) ([]AntigravityHook, error) {
	path := AntigravityHooksPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := decodeObject(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	names := make([]string, 0, len(doc))
	for name := range doc {
		names = append(names, name)
	}
	slices.Sort(names)

	var found []AntigravityHook
	for _, name := range names {
		raw := doc[name]
		var set agySet
		if json.Unmarshal(raw, &set) != nil {
			continue
		}
		for _, group := range set.PreToolUse {
			for _, h := range group.Hooks {
				if exe, ok := parseIronCommand(h.Command, antigravityHookArg); ok {
					found = append(found, AntigravityHook{
						Set: name, Exe: exe, Matcher: group.Matcher, Timeout: h.Timeout,
						Enabled: set.Enabled == nil || *set.Enabled,
					})
				}
			}
		}
	}
	return found, nil
}

// MatcherCovers diz se o matcher do hooks.json pega a ferramenta: vazio e "*"
// pegam tudo; o resto é regex, que precisa casar o nome inteiro (o agy aceita
// nome exato, "a|b" e regex).
func MatcherCovers(matcher, tool string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	return err == nil && re.MatchString(tool)
}
