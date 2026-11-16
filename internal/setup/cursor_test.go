package setup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCursorFromScratch(t *testing.T) {
	dir := t.TempDir()
	state, err := InstallCursor(dir, "/opt/iron")
	if err != nil || state != Installed {
		t.Fatalf("estado %v, erro %v", state, err)
	}
	doc := readJSON(t, filepath.Join(dir, ".cursor/hooks.json"))
	h := doc["hooks"].(map[string]any)["beforeShellExecution"].([]any)[0].(map[string]any)
	// failClosed é o que faz o hook bloquear quando falha ou estoura o prazo.
	if doc["version"] != float64(1) || h["command"] != `'/opt/iron' hook --agent=cursor` ||
		h["failClosed"] != true || h["timeout"] != float64(600) {
		t.Errorf("arquivo inesperado: %v", doc)
	}
}

func TestInstallCursorIdempotentAndUpdates(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallCursor(dir, "/opt/iron"); err != nil {
		t.Fatal(err)
	}
	path := CursorHooksPath(dir)
	before, _ := os.ReadFile(path)

	if state, err := InstallCursor(dir, "/opt/iron"); err != nil || state != Already {
		t.Errorf("segunda vez deveria ser Already: %v %v", state, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("o arquivo mudou na segunda execução")
	}

	if state, err := InstallCursor(dir, "/novo/iron"); err != nil || state != Updated {
		t.Errorf("caminho novo deveria ser Updated: %v %v", state, err)
	}
	found, err := FindCursor(dir)
	if err != nil || len(found) != 1 || found[0].Exe != "/novo/iron" || found[0].Timeout != 600 || !found[0].FailClosed {
		t.Errorf("deveria haver um só hook, com o caminho novo: %+v %v", found, err)
	}
}

// Um hook nosso gravado sem failClosed (à mão, ou por outra versão) é corrigido.
func TestInstallCursorFixesMissingFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := CursorHooksPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"version":1,"hooks":{"beforeShellExecution":[{"command":"'/opt/iron' hook --agent=cursor"}]}}`), 0o644)
	if state, err := InstallCursor(dir, "/opt/iron"); err != nil || state != Updated {
		t.Fatalf("estado %v, erro %v", state, err)
	}
	found, _ := FindCursor(dir)
	if len(found) != 1 || !found[0].FailClosed || found[0].Timeout != 600 {
		t.Errorf("deveria ter failClosed e prazo: %+v", found)
	}
}

// O que a pessoa já tem no arquivo (outro hook, outro evento, campos extras) fica.
func TestInstallCursorKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	path := CursorHooksPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	original := `{"version":1,"extra":true,"hooks":{"afterFileEdit":[{"command":".cursor/fmt.sh"}],` +
		`"beforeShellExecution":[{"command":".cursor/audita.sh","matcher":"git","failClosed":true}]}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := InstallCursor(dir, "/opt/iron"); err != nil || state != Installed {
		t.Fatalf("estado %v, erro %v", state, err)
	}
	text, _ := os.ReadFile(path)
	for _, want := range []string{`"extra": true`, `"afterFileEdit"`, `.cursor/audita.sh`, `"matcher": "git"`, `hook --agent=cursor`} {
		if !strings.Contains(string(text), want) {
			t.Errorf("perdeu %s:\n%s", want, text)
		}
	}
}

func TestInstallCursorFixesMissingVersion(t *testing.T) {
	dir := t.TempDir()
	path := CursorHooksPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"hooks":{}}`), 0o644)
	if state, err := InstallCursor(dir, "/opt/iron"); err != nil || state != Updated {
		t.Fatalf("estado %v, erro %v", state, err)
	}
	if doc := readJSON(t, path); doc["version"] != float64(1) {
		t.Errorf("sem version 1 o arquivo não vale: %v", doc)
	}
}

func TestInstallCursorRefusesBrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := CursorHooksPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	for name, content := range map[string]string{
		"json quebrado":    `{`,
		"hooks não objeto": `{"version":1,"hooks":[]}`,
		"evento não lista": `{"version":1,"hooks":{"beforeShellExecution":{}}}`,
	} {
		_ = os.WriteFile(path, []byte(content), 0o644)
		if _, err := InstallCursor(dir, "/opt/iron"); err == nil || !strings.Contains(err.Error(), "hooks.json") {
			t.Errorf("%s: deveria recusar citando o arquivo, obtive %v", name, err)
		}
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Errorf("%s: o arquivo da pessoa foi alterado", name)
		}
	}
}

func TestFindCursor(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindCursor(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("sem arquivo deveria ser ErrNotExist: %v", err)
	}
	path := CursorHooksPath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	// um programa que não se chama iron nunca é tratado como nosso.
	_ = os.WriteFile(path, []byte(`{"version":1,"hooks":{"beforeShellExecution":[`+
		`{"command":"'/bin/rm' hook --agent=cursor"},{"command":".cursor/x.sh"},`+
		`{"command":"'/opt/iron' hook --agent=cursor","timeout":30}]}}`), 0o644)
	found, err := FindCursor(dir)
	if err != nil || len(found) != 1 || found[0].Exe != "/opt/iron" || found[0].Timeout != 30 || found[0].FailClosed {
		t.Errorf("achou %+v, erro %v", found, err)
	}
	_ = os.WriteFile(path, []byte(`{`), 0o644)
	if _, err := FindCursor(dir); err == nil {
		t.Error("JSON inválido deveria ser erro")
	}
}
