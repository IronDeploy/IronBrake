package setup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// Codex CLI (verificado com o 0.159.3): o hook de projeto fica em
// .codex/hooks.json, {"hooks":{"PreToolUse":[{matcher,hooks:[{type,command,timeout}]}]}}.
// O Codex só roda um hook depois de a pasta e o hook (por hash) estarem
// confiados no ~/.codex/config.toml, o que a pessoa faz em /hooks. Um hook
// alterado volta a exigir revisão, e no "codex exec" nada avisa.
const (
	codexHooksFile  = ".codex/hooks.json"
	codexConfigFile = ".codex/config.toml"
	codexHookArg    = "--agent=codex"
)

// CodexHooksPath é o hooks.json de projeto.
func CodexHooksPath(dir string) string { return filepath.Join(dir, codexHooksFile) }

// CodexConfigPath é o config.toml de projeto.
func CodexConfigPath(dir string) string { return filepath.Join(dir, codexConfigFile) }

type codexHandler struct {
	Type    string  `json:"type"`
	Command string  `json:"command"`
	Timeout float64 `json:"timeout,omitempty"`
}

type codexGroup struct {
	Matcher string         `json:"matcher"`
	Hooks   []codexHandler `json:"hooks"`
}

func codexOurGroup(exePath string) codexGroup {
	return codexGroup{Matcher: hook.CodexShellMatcher, Hooks: []codexHandler{{
		Type: "command", Command: ironCommand(exePath, codexHookArg), Timeout: hook.CodexTimeout.Seconds(),
	}}}
}

func isCodexOurs(raw json.RawMessage) bool {
	var g codexGroup
	if json.Unmarshal(raw, &g) != nil {
		return false
	}
	for _, h := range g.Hooks {
		if _, ok := parseIronCommand(h.Command, codexHookArg); ok {
			return true
		}
	}
	return false
}

// loadCodexHooks lê o hooks.json; devolve o documento, o objeto "hooks" e a
// lista de grupos do PreToolUse.
func loadCodexHooks(path string) (doc, hooks map[string]json.RawMessage, groups []json.RawMessage, err error) {
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
	if raw := hooks["PreToolUse"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, nil, nil, fmt.Errorf(`%s: a chave "hooks.PreToolUse" não é uma lista`, path)
		}
	}
	return doc, hooks, groups, nil
}

// InstallCodex garante o hook do Iron Brake em dir/.codex/hooks.json sem mexer
// no resto. Um hook nosso com outro caminho, matcher ou prazo é substituído
// no lugar (e o Codex pede a confiança de novo).
func InstallCodex(dir, exePath string) (InstallState, error) {
	path := CodexHooksPath(dir)
	doc, hooks, groups, err := loadCodexHooks(path)
	state := Installed
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc, hooks = map[string]json.RawMessage{}, map[string]json.RawMessage{}
	case err != nil:
		return 0, err
	}

	want, err := encode(codexOurGroup(exePath), "")
	if err != nil {
		return 0, err
	}
	replaced := false
	for i, raw := range groups {
		if !isCodexOurs(raw) {
			continue
		}
		if sameJSON(raw, want) {
			return Already, nil
		}
		groups[i], replaced, state = want, true, Updated
		break
	}
	if !replaced {
		groups = append(groups, want)
	}

	if hooks["PreToolUse"], err = encode(groups, ""); err != nil {
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

// CodexHook é um hook do Iron Brake achado no hooks.json.
type CodexHook struct {
	Exe     string
	Matcher string
	Timeout float64 // segundos; 0 = o campo não existe (padrão do Codex: 600 s)
	Key     string  // chave da confiança no config.toml: <hooks.json>:pre_tool_use:<grupo>:<handler>
}

// FindCodex lista os hooks do Iron Brake de dir/.codex/hooks.json. Sem arquivo,
// o erro satisfaz errors.Is(err, fs.ErrNotExist).
func FindCodex(dir string) ([]CodexHook, error) {
	path := CodexHooksPath(dir)
	_, _, groups, err := loadCodexHooks(path)
	if err != nil {
		return nil, err
	}
	var found []CodexHook
	for gi, raw := range groups {
		var g codexGroup
		if json.Unmarshal(raw, &g) != nil {
			continue
		}
		for hi, h := range g.Hooks {
			if exe, ok := parseIronCommand(h.Command, codexHookArg); ok {
				found = append(found, CodexHook{
					Exe: exe, Matcher: g.Matcher, Timeout: h.Timeout,
					Key: fmt.Sprintf("%s:pre_tool_use:%d:%d", path, gi, hi),
				})
			}
		}
	}
	return found, nil
}

// CodexTrust é o estado de confiança lido do ~/.codex/config.toml.
type CodexTrust struct {
	ProjectTrusted bool
	TrustedHooks   map[string]bool // chaves com trusted_hash
	ConfigModTime  time.Time       // quando o config.toml foi gravado pela última vez
}

var (
	tomlHeader = regexp.MustCompile(`^\[\s*(projects|hooks\.state)\s*\.\s*"((?:[^"\\]|\\.)*)"\s*\]\s*$`)
	tomlString = regexp.MustCompile(`^\s*([A-Za-z_]+)\s*=\s*"((?:[^"\\]|\\.)*)"`)
)

func tomlUnescape(s string) string {
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(s)
}

// ReadCodexTrust lê a confiança de dir (pasta) no config.toml do usuário. Sem o
// arquivo, nada está confiado (e o erro é nil).
func ReadCodexTrust(configPath, dir string) (CodexTrust, error) {
	trust := CodexTrust{TrustedHooks: map[string]bool{}}
	f, err := os.Open(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return trust, nil
	}
	if err != nil {
		return trust, err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil {
		trust.ConfigModTime = info.ModTime()
	}

	root := filepath.Clean(dir)
	var kind, name string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := tomlHeader.FindStringSubmatch(line); m != nil {
			kind, name = m[1], tomlUnescape(m[2])
			continue
		}
		if strings.HasPrefix(line, "[") {
			kind = ""
			continue
		}
		m := tomlString.FindStringSubmatch(line)
		switch {
		case m == nil:
		case kind == "projects" && m[1] == "trust_level" && m[2] == "trusted" && filepath.Clean(name) == root:
			trust.ProjectTrusted = true
		case kind == "hooks.state" && m[1] == "trusted_hash" && m[2] != "":
			trust.TrustedHooks[name] = true
		}
	}
	return trust, scanner.Err()
}

// TagState diz o que o instalador fez com a etiqueta da AWS.
type TagState int

const (
	TagCreated   TagState = iota // criou o .codex/config.toml com a etiqueta
	TagAlready                   // o arquivo já traz a etiqueta com este valor
	TagDifferent                 // o arquivo traz AWS_SDK_UA_APP_ID com outro valor: não mexe
	TagManual                    // o arquivo existe sem a etiqueta: não edita TOML da pessoa
)

// CodexTagSnippet é o trecho que a pessoa acrescenta quando o config.toml já existe.
func CodexTagSnippet(appID string) string {
	return fmt.Sprintf("[shell_environment_policy]\nset = { AWS_SDK_UA_APP_ID = %q }\n", appID)
}

// EnsureCodexAWSTag grava a etiqueta do agente para os comandos do Codex
// (shell_environment_policy.set, verificado no exec e no chat). Só cria o
// arquivo; num config.toml existente não edita, porque não há leitor de TOML.
func EnsureCodexAWSTag(dir, appID string) (TagState, error) {
	path := CodexConfigPath(dir)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return 0, err
		}
		return TagCreated, writeFileAtomic(path, []byte(CodexTagSnippet(appID)))
	case err != nil:
		return 0, err
	}
	text := string(data)
	switch {
	case strings.Contains(text, "AWS_SDK_UA_APP_ID") && strings.Contains(text, `"`+appID+`"`):
		return TagAlready, nil
	case strings.Contains(text, "AWS_SDK_UA_APP_ID"):
		return TagDifferent, nil
	}
	return TagManual, nil
}
