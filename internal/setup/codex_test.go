package setup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCodexFromScratch(t *testing.T) {
	dir := t.TempDir()
	if state, err := InstallCodex(dir, "/opt/iron"); err != nil || state != Installed {
		t.Fatalf("estado %v, erro %v", state, err)
	}
	doc := readJSON(t, CodexHooksPath(dir))
	group := doc["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	h := group["hooks"].([]any)[0].(map[string]any)
	if group["matcher"] != "^Bash$" || h["type"] != "command" ||
		h["command"] != `'/opt/iron' hook --agent=codex` || h["timeout"] != float64(600) {
		t.Errorf("hooks.json inesperado: %v", doc)
	}
}

func TestInstallCodexIdempotentAndUpdates(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallCodex(dir, "/opt/iron"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(CodexHooksPath(dir))
	if state, err := InstallCodex(dir, "/opt/iron"); err != nil || state != Already {
		t.Errorf("segunda vez deveria ser Already: %v %v", state, err)
	}
	if after, _ := os.ReadFile(CodexHooksPath(dir)); string(after) != string(before) {
		t.Error("o arquivo mudou na segunda execução (mudar o arquivo faz o Codex pedir a confiança de novo)")
	}

	if state, err := InstallCodex(dir, "/novo/iron"); err != nil || state != Updated {
		t.Errorf("caminho novo deveria ser Updated: %v %v", state, err)
	}
	found, _ := FindCodex(dir)
	if len(found) != 1 || found[0].Exe != "/novo/iron" {
		t.Errorf("deveria haver um só hook, com o caminho novo: %+v", found)
	}
}

func TestInstallCodexKeepsOtherHooks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, CodexHooksPath(dir), `{"hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"./lint.sh"}]}],
	  "PreToolUse":[{"matcher":"^mcp__","hooks":[{"type":"command","command":"./mcp-check.sh","timeout":5}]}]}}`)
	if _, err := InstallCodex(dir, "/opt/iron"); err != nil {
		t.Fatal(err)
	}
	hooks := readJSON(t, CodexHooksPath(dir))["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 2 || len(hooks["PostToolUse"].([]any)) != 1 {
		t.Errorf("o conteúdo da pessoa deveria ser mantido: %v", hooks)
	}
	if pre[0].(map[string]any)["matcher"] != "^mcp__" {
		t.Error("o hook anterior deve continuar primeiro")
	}
	found, _ := FindCodex(dir)
	if len(found) != 1 || !strings.HasSuffix(found[0].Key, ":pre_tool_use:1:0") {
		t.Errorf("a chave da confiança usa a posição do grupo (1) e do handler (0): %+v", found)
	}
}

func TestInstallCodexBrokenFileIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, CodexHooksPath(dir), `{ "hooks": `)
	if _, err := InstallCodex(dir, "/opt/iron"); err == nil || !strings.Contains(err.Error(), "hooks.json") {
		t.Errorf("deveria falhar citando o arquivo: %v", err)
	}
	if data, _ := os.ReadFile(CodexHooksPath(dir)); string(data) != `{ "hooks": ` {
		t.Error("o arquivo da pessoa não pode ser sobrescrito")
	}
}

func TestFindCodex(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindCodex(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("sem arquivo o erro deveria ser ErrNotExist: %v", err)
	}
	writeFile(t, CodexHooksPath(dir), `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"command":"'/x/iron' hook --agent=codex"},{"command":"/bin/sh hook --agent=codex"},{"command":"'/y/iron' hook --agent=claude"}]}]}}`)
	found, err := FindCodex(dir)
	if err != nil || len(found) != 1 || found[0].Exe != "/x/iron" || found[0].Timeout != 0 || found[0].Matcher != "*" {
		t.Errorf("só o hook do Iron Brake com o programa iron conta: %+v %v", found, err)
	}
}

// config.toml com o formato que o Codex 0.159.3 gravou de verdade.
const realCodexConfig = `[tui]
screen_reader_detection_done = true

[projects."/home/dev/projeto"]
trust_level = "trusted"

[projects."/home/dev/outro"]
trust_level = "untrusted"

[hooks.state]

[hooks.state."/home/dev/projeto/.codex/hooks.json:pre_tool_use:0:0"]
trusted_hash = "sha256:5ceacb6b5c65d3e1c18f33934b4ecc4c9fad922af40193e2b1eda29d4eff5716"
`

func TestReadCodexTrust(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, cfg, realCodexConfig)

	trust, err := ReadCodexTrust(cfg, "/home/dev/projeto/")
	if err != nil || !trust.ProjectTrusted || !trust.TrustedHooks["/home/dev/projeto/.codex/hooks.json:pre_tool_use:0:0"] || trust.ConfigModTime.IsZero() {
		t.Errorf("deveria ler a confiança real: %+v %v", trust, err)
	}
	if other, _ := ReadCodexTrust(cfg, "/home/dev/outro"); other.ProjectTrusted {
		t.Error("trust_level diferente de trusted não conta")
	}
	if none, _ := ReadCodexTrust(cfg, "/home/dev/sem-nada"); none.ProjectTrusted || len(none.TrustedHooks) != 1 {
		t.Errorf("pasta desconhecida: %+v", none)
	}
	if missing, err := ReadCodexTrust(filepath.Join(t.TempDir(), "nao-existe.toml"), "/x"); err != nil || missing.ProjectTrusted {
		t.Errorf("sem arquivo nada está confiado e não é erro: %+v %v", missing, err)
	}
}

func TestReadCodexTrustEscapedPathAndEmptyHash(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, cfg, `[projects."/home/o\"neil/p"]
trust_level = "trusted"

[hooks.state."/x/.codex/hooks.json:pre_tool_use:0:0"]
trusted_hash = ""
`)
	trust, _ := ReadCodexTrust(cfg, `/home/o"neil/p`)
	if !trust.ProjectTrusted || len(trust.TrustedHooks) != 0 {
		t.Errorf("aspas escapadas valem; hash vazio não confia: %+v", trust)
	}
}

func TestEnsureCodexAWSTag(t *testing.T) {
	dir := t.TempDir()
	if state, err := EnsureCodexAWSTag(dir, "iron-codex"); err != nil || state != TagCreated {
		t.Fatalf("deveria criar: %v %v", state, err)
	}
	data, _ := os.ReadFile(CodexConfigPath(dir))
	if string(data) != "[shell_environment_policy]\nset = { AWS_SDK_UA_APP_ID = \"iron-codex\" }\n" {
		t.Errorf("config.toml inesperado: %q", data)
	}
	if state, _ := EnsureCodexAWSTag(dir, "iron-codex"); state != TagAlready {
		t.Errorf("segunda vez: %v", state)
	}

	writeFile(t, CodexConfigPath(dir), "[shell_environment_policy]\nset = { AWS_SDK_UA_APP_ID = \"outro\" }\n")
	if state, _ := EnsureCodexAWSTag(dir, "iron-codex"); state != TagDifferent {
		t.Errorf("valor da pessoa: %v", state)
	}
	manual := "model = \"x\"\n"
	writeFile(t, CodexConfigPath(dir), manual)
	if state, _ := EnsureCodexAWSTag(dir, "iron-codex"); state != TagManual {
		t.Errorf("arquivo existente sem a etiqueta: %v", state)
	}
	if data, _ := os.ReadFile(CodexConfigPath(dir)); string(data) != manual {
		t.Error("não pode editar o TOML da pessoa")
	}
}
