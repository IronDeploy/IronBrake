package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

// Kiro CLI: dois formatos, um por engine (verificado com o Kiro 2.26.1).
//
//   - v2 lê o hook só de dentro do agente (.kiro/agents/*.json) e ignora o
//     .kiro/hooks. Vale só para o agente que o contém, então o instalador
//     acrescenta o hook a cada agente da pasta e, se não houver, cria um
//     kiro_default.json (um kiro_default no projeto substitui o padrão).
//   - v3 lê o .kiro/hooks/*.json, que vale para qualquer agente. Agente em
//     formato v2 ele não carrega ("needs upgrading").
const (
	kiroAgentsDir     = ".kiro/agents"
	kiroHooksFile     = ".kiro/hooks/iron-brake.json"
	kiroDefaultAgent  = "kiro_default"
	kiroMatcherV2     = "shell"
	kiroHookArg       = "--agent=kiro"
	kiroTimeoutMillis = int64(hook.KiroTimeout / 1e6)
	kiroTimeoutSecs   = int64(hook.KiroTimeout / 1e9)
)

// KiroResult diz o que o instalador mexeu, com caminhos relativos à pasta.
type KiroResult struct {
	Changed []string // arquivos criados ou alterados
	Already []string // arquivos que já tinham o hook
	Skipped []string // agentes em formato v3: o .kiro/hooks já os cobre
}

// kiroCommand é a linha que o Kiro executa: o caminho vai entre aspas simples
// porque o command do Kiro é texto (passa por um shell), não exec com args.
func kiroCommand(exePath string) string {
	return shellQuote(exePath) + " " + HookSubcommand + " " + kiroHookArg
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// InstallKiro instala o hook nos dois formatos. Qualquer arquivo ilegível
// interrompe e é citado no erro.
func InstallKiro(dir, exePath string) (KiroResult, error) {
	var res KiroResult
	cmd := kiroCommand(exePath)

	files, err := filepath.Glob(filepath.Join(dir, kiroAgentsDir, "*.json"))
	if err != nil {
		return res, err
	}
	hasDefault := false
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		changed, v3, name, err := mergeKiroAgent(f, cmd)
		if err != nil {
			return res, err
		}
		if name == kiroDefaultAgent {
			hasDefault = true
		}
		switch {
		case v3:
			res.Skipped = append(res.Skipped, rel)
		case changed:
			res.Changed = append(res.Changed, rel)
		default:
			res.Already = append(res.Already, rel)
		}
	}

	if !hasDefault {
		path := filepath.Join(dir, kiroAgentsDir, kiroDefaultAgent+".json")
		if err := writeNewKiroDefault(path, cmd); err != nil {
			return res, err
		}
		res.Changed = append(res.Changed, filepath.Join(kiroAgentsDir, kiroDefaultAgent+".json"))
	}

	changed, err := mergeKiroHooksFile(filepath.Join(dir, kiroHooksFile), cmd)
	if err != nil {
		return res, err
	}
	if changed {
		res.Changed = append(res.Changed, kiroHooksFile)
	} else {
		res.Already = append(res.Already, kiroHooksFile)
	}
	return res, nil
}

// mergeKiroAgent acrescenta o hook a um agente em formato v2. v3 é true para
// agente em formato v3 (hooks é uma lista), que não é alterado.
func mergeKiroAgent(path, cmd string) (changed, v3 bool, name string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false, "", err
	}
	agent, err := decodeObject(data)
	if err != nil {
		return false, false, "", fmt.Errorf("%s: %w", path, err)
	}
	_ = json.Unmarshal(agent["name"], &name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".json")
	}

	if raw := agent["hooks"]; len(raw) > 0 && strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		return false, true, name, nil
	}
	hooks, err := decodeObject(agent["hooks"])
	if err != nil {
		return false, false, name, fmt.Errorf(`%s: a chave "hooks" %w`, path, err)
	}

	var pre []json.RawMessage
	if raw := hooks["preToolUse"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &pre); err != nil {
			return false, false, name, fmt.Errorf(`%s: a chave "hooks.preToolUse" não é uma lista`, path)
		}
	}
	for _, raw := range pre {
		var h struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(raw, &h) == nil && h.Command == cmd {
			return false, false, name, nil
		}
	}

	entry, err := encode(map[string]any{"matcher": kiroMatcherV2, "command": cmd, "timeout_ms": kiroTimeoutMillis}, "")
	if err != nil {
		return false, false, name, err
	}
	if hooks["preToolUse"], err = encode(append(pre, entry), ""); err != nil {
		return false, false, name, err
	}
	if agent["hooks"], err = encode(hooks, ""); err != nil {
		return false, false, name, err
	}
	out, err := encode(agent, "  ")
	if err != nil {
		return false, false, name, err
	}
	return true, false, name, writeFileAtomic(path, append(out, '\n'))
}

// writeNewKiroDefault cria o agente padrão do projeto. Ele substitui o padrão
// do Kiro (sem o prompt longo dele), porque o v2 só aplica hook de agente.
func writeNewKiroDefault(path, cmd string) error {
	doc := map[string]any{
		"name":        kiroDefaultAgent,
		"description": "Agente padrão do projeto, com o hook do Iron Brake",
		"tools":       []string{"*"},
		"hooks": map[string]any{
			"preToolUse": []any{map[string]any{"matcher": kiroMatcherV2, "command": cmd, "timeout_ms": kiroTimeoutMillis}},
		},
	}
	out, err := encode(doc, "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, append(out, '\n'))
}

// kiroHook é uma entrada de .kiro/hooks/*.json (v3).
type kiroHook struct {
	Name    string `json:"name"`
	Trigger string `json:"trigger"`
	Matcher string `json:"matcher"`
	Action  struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"action"`
	Timeout float64 `json:"timeout,omitempty"`
	Enabled bool    `json:"enabled"`
}

func mergeKiroHooksFile(path, cmd string) (bool, error) {
	doc := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return false, err
	default:
		if doc, err = decodeObject(data); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
	}

	var list []json.RawMessage
	if raw := doc["hooks"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &list); err != nil {
			return false, fmt.Errorf(`%s: a chave "hooks" não é uma lista`, path)
		}
	}
	for _, raw := range list {
		var h kiroHook
		if json.Unmarshal(raw, &h) == nil && h.Action.Command == cmd {
			return false, nil
		}
	}

	ours := kiroHook{Name: "iron-brake", Trigger: "PreToolUse", Matcher: hook.KiroShellMatcherV3, Timeout: float64(kiroTimeoutSecs), Enabled: true}
	ours.Action.Type, ours.Action.Command = "command", cmd
	entry, err := encode(ours, "")
	if err != nil {
		return false, err
	}
	if doc["hooks"], err = encode(append(list, entry), ""); err != nil {
		return false, err
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = json.RawMessage(`"v1"`)
	}
	out, err := encode(doc, "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, writeFileAtomic(path, append(out, '\n'))
}

// KiroHook é um hook do Iron Brake achado numa configuração do Kiro.
type KiroHook struct {
	File    string  // relativo à pasta do projeto
	Exe     string  // programa, já sem as aspas
	Timeout float64 // segundos; 0 = o campo não existe (padrão do Kiro: ~10 s no v2)
}

// KiroHooks é o que o doctor confere: onde está o hook de cada engine.
type KiroHooks struct {
	V2 []KiroHook // agentes em formato v2 (.kiro/agents) com o hook
	V3 []KiroHook // .kiro/hooks/*.json com o hook

	// Doubled: agentes em formato v3 que também têm o hook. Com o .kiro/hooks
	// ele roda duas vezes por comando (o v3 carrega os dois).
	Doubled []string
}

// FindKiro lista os hooks do Iron Brake da pasta. Arquivo ilegível é erro; os
// que não têm o hook são ignorados.
func FindKiro(dir string) (KiroHooks, error) {
	var found KiroHooks

	agents, err := filepath.Glob(filepath.Join(dir, kiroAgentsDir, "*.json"))
	if err != nil {
		return found, err
	}
	for _, f := range agents {
		rel, _ := filepath.Rel(dir, f)
		data, err := os.ReadFile(f)
		if err != nil {
			return found, err
		}
		agent, err := decodeObject(data)
		if err != nil {
			return found, fmt.Errorf("%s: %w", f, err)
		}
		raw := strings.TrimSpace(string(agent["hooks"]))
		if strings.HasPrefix(raw, "[") {
			var list []kiroHook
			if json.Unmarshal([]byte(raw), &list) == nil {
				for _, h := range list {
					if _, ok := parseKiroCommand(h.Action.Command); ok {
						found.Doubled = append(found.Doubled, rel)
						break
					}
				}
			}
			continue
		}
		hooks, err := decodeObject(agent["hooks"])
		if err != nil {
			continue
		}
		var pre []struct {
			Command   string  `json:"command"`
			TimeoutMs float64 `json:"timeout_ms"`
		}
		if json.Unmarshal(hooks["preToolUse"], &pre) != nil {
			continue
		}
		for _, h := range pre {
			if exe, ok := parseKiroCommand(h.Command); ok {
				found.V2 = append(found.V2, KiroHook{File: rel, Exe: exe, Timeout: h.TimeoutMs / 1000})
				break
			}
		}
	}

	files, err := filepath.Glob(filepath.Join(dir, ".kiro/hooks", "*.json"))
	if err != nil {
		return found, err
	}
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		data, err := os.ReadFile(f)
		if err != nil {
			return found, err
		}
		doc, err := decodeObject(data)
		if err != nil {
			return found, fmt.Errorf("%s: %w", f, err)
		}
		var list []kiroHook
		if json.Unmarshal(doc["hooks"], &list) != nil {
			continue
		}
		for _, h := range list {
			if exe, ok := parseKiroCommand(h.Action.Command); ok && h.Enabled && h.Trigger == "PreToolUse" {
				found.V3 = append(found.V3, KiroHook{File: rel, Exe: exe, Timeout: h.Timeout})
				break
			}
		}
	}
	return found, nil
}

// parseKiroCommand desfaz o kiroCommand: '<caminho>' hook --agent=kiro. Só
// reconhece programas chamados iron, para o doctor nunca executar outro.
func parseKiroCommand(cmd string) (string, bool) {
	suffix := " " + HookSubcommand + " " + kiroHookArg
	body, ok := strings.CutSuffix(cmd, suffix)
	if !ok || len(body) < 2 || body[0] != '\'' || body[len(body)-1] != '\'' {
		return "", false
	}
	inner := body[1 : len(body)-1]
	// Depois de tirar as aspas escapadas, sobra aspa solta = o texto não é só um caminho.
	if strings.Contains(strings.ReplaceAll(inner, `'\''`, ""), "'") {
		return "", false
	}
	exe := strings.ReplaceAll(inner, `'\''`, "'")
	if !IsIronBinaryName(exe) {
		return "", false
	}
	return exe, true
}
