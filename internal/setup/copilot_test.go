package setup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCopilotFromScratch(t *testing.T) {
	dir := t.TempDir()
	state, err := InstallCopilot(dir, "/opt/iron")
	if err != nil || state != Installed {
		t.Fatalf("estado %v, erro %v", state, err)
	}

	doc := readJSON(t, filepath.Join(dir, ".github/hooks/iron-brake.json"))
	h := doc["hooks"].(map[string]any)["preToolUse"].([]any)[0].(map[string]any)
	if doc["version"] != float64(1) || h["type"] != "command" ||
		h["bash"] != `'/opt/iron' hook --agent=copilot` ||
		h["powershell"] != `& '/opt/iron' hook --agent=copilot` || h["timeoutSec"] != float64(600) {
		t.Errorf("arquivo inesperado: %v", doc)
	}
}

func TestInstallCopilotPowerShellQuoting(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallCopilot(dir, `C:\Users\O'Neil\iron.exe`); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, filepath.Join(dir, ".github/hooks/iron-brake.json"))
	h := doc["hooks"].(map[string]any)["preToolUse"].([]any)[0].(map[string]any)
	if h["powershell"] != `& 'C:\Users\O''Neil\iron.exe' hook --agent=copilot` {
		t.Errorf("aspa simples deveria ser dobrada: %v", h["powershell"])
	}
}

func TestInstallCopilotIdempotentAndUpdates(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallCopilot(dir, "/opt/iron"); err != nil {
		t.Fatal(err)
	}
	path := CopilotHooksPath(dir)
	before, _ := os.ReadFile(path)

	if state, err := InstallCopilot(dir, "/opt/iron"); err != nil || state != Already {
		t.Errorf("segunda vez deveria ser Already: %v %v", state, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("o arquivo mudou na segunda execução")
	}

	if state, err := InstallCopilot(dir, "/novo/iron"); err != nil || state != Updated {
		t.Errorf("caminho novo deveria ser Updated: %v %v", state, err)
	}
	found, err := FindCopilot(dir)
	if err != nil || len(found) != 1 || found[0].Exe != "/novo/iron" || found[0].Timeout != 600 {
		t.Errorf("deveria haver um só hook, com o caminho novo: %+v %v", found, err)
	}
}

// O que a pessoa já tem no arquivo (outro hook, outro evento, campos extras) fica.
func TestInstallCopilotKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	path := CopilotHooksPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"version":1,"extra":true,"hooks":{"sessionStart":[{"type":"command","bash":"echo oi"}],` +
		`"preToolUse":[{"type":"command","bash":"./audita.sh","cwd":"scripts"}]}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := InstallCopilot(dir, "/opt/iron"); err != nil || state != Installed {
		t.Fatalf("estado %v, erro %v", state, err)
	}
	text, _ := os.ReadFile(path)
	for _, want := range []string{`"extra": true`, `"sessionStart"`, `./audita.sh`, `"cwd": "scripts"`, `hook --agent=copilot`} {
		if !strings.Contains(string(text), want) {
			t.Errorf("perdeu %s:\n%s", want, text)
		}
	}
}

func TestInstallCopilotFixesMissingVersion(t *testing.T) {
	dir := t.TempDir()
	path := CopilotHooksPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"hooks":{}}`), 0o644)
	if state, err := InstallCopilot(dir, "/opt/iron"); err != nil || state != Updated {
		t.Fatalf("estado %v, erro %v", state, err)
	}
	if doc := readJSON(t, path); doc["version"] != float64(1) {
		t.Errorf("sem version 1 o Copilot não carrega o arquivo: %v", doc)
	}
}

func TestInstallCopilotRefusesBrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := CopilotHooksPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	for name, content := range map[string]string{
		"json quebrado":        `{`,
		"hooks não objeto":     `{"version":1,"hooks":[]}`,
		"preToolUse não lista": `{"version":1,"hooks":{"preToolUse":{}}}`,
	} {
		_ = os.WriteFile(path, []byte(content), 0o644)
		if _, err := InstallCopilot(dir, "/opt/iron"); err == nil || !strings.Contains(err.Error(), "iron-brake.json") {
			t.Errorf("%s: deveria recusar citando o arquivo, obtive %v", name, err)
		}
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Errorf("%s: o arquivo da pessoa foi alterado", name)
		}
	}
}

func TestFindCopilotScansEveryFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindCopilot(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("sem arquivos deveria ser ErrNotExist: %v", err)
	}
	hooksDir := filepath.Join(dir, ".github/hooks")
	_ = os.MkdirAll(hooksDir, 0o755)
	// um arquivo de outra ferramenta, e o nosso com outro nome.
	_ = os.WriteFile(filepath.Join(hooksDir, "a-outro.json"), []byte(`{"version":1,"hooks":{"preToolUse":[{"type":"command","bash":"./x.sh"}]}}`), 0o644)
	_ = os.WriteFile(filepath.Join(hooksDir, "b-nosso.json"),
		[]byte(`{"version":1,"hooks":{"preToolUse":[{"type":"command","bash":"'/opt/iron' hook --agent=copilot","timeoutSec":30}]}}`), 0o644)
	found, err := FindCopilot(dir)
	if err != nil || len(found) != 1 || found[0].Exe != "/opt/iron" || found[0].Timeout != 30 ||
		filepath.Base(found[0].File) != "b-nosso.json" {
		t.Errorf("achou %+v, erro %v", found, err)
	}

	// um programa que não se chama iron nunca é tratado como nosso.
	_ = os.WriteFile(filepath.Join(hooksDir, "c-falso.json"),
		[]byte(`{"version":1,"hooks":{"preToolUse":[{"type":"command","bash":"'/bin/rm' hook --agent=copilot"}]}}`), 0o644)
	if found, _ := FindCopilot(dir); len(found) != 1 {
		t.Errorf("o falso não deveria contar: %+v", found)
	}

	// JSON inválido é erro (o Copilot não carrega o arquivo).
	_ = os.WriteFile(filepath.Join(hooksDir, "d-quebrado.json"), []byte(`{`), 0o644)
	if _, err := FindCopilot(dir); err == nil {
		t.Error("JSON inválido deveria ser erro")
	}
}

func TestGitRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := GitRoot(sub); got != "" && !strings.HasPrefix(root, got) {
		// fora de um repositório (a pasta temporária pode estar dentro de um, raro).
		t.Logf("a pasta temporária está dentro de um repositório: %s", got)
	}
	// .git como pasta e como arquivo (worktree) marcam a raiz.
	for _, mk := range []func(string) error{
		func(p string) error { return os.Mkdir(p, 0o755) },
		func(p string) error { return os.WriteFile(p, []byte("gitdir: ../x"), 0o644) },
	} {
		_ = os.RemoveAll(filepath.Join(root, ".git"))
		if err := mk(filepath.Join(root, ".git")); err != nil {
			t.Fatal(err)
		}
		if got := GitRoot(sub); got != root {
			t.Errorf("GitRoot(%s) = %q; esperava %q", sub, got, root)
		}
		if got := GitRoot(root); got != root {
			t.Errorf("na própria raiz: %q", got)
		}
	}
}
