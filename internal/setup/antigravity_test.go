package setup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAntigravityFromScratch(t *testing.T) {
	dir := t.TempDir()
	state, err := InstallAntigravity(dir, "/opt/iron")
	if err != nil || state != Installed {
		t.Fatalf("estado %v, erro %v", state, err)
	}

	doc := readJSON(t, filepath.Join(dir, ".agents/hooks.json"))
	group := doc["iron-brake"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	h := group["hooks"].([]any)[0].(map[string]any)
	if group["matcher"] != "run_command" || h["type"] != "command" ||
		h["command"] != `'/opt/iron' hook --agent=antigravity` || h["timeout"] != float64(600) {
		t.Errorf("hooks.json inesperado: %v", doc)
	}
}

func TestInstallAntigravityIdempotentAndUpdates(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallAntigravity(dir, "/opt/iron"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".agents/hooks.json")
	before, _ := os.ReadFile(path)

	if state, err := InstallAntigravity(dir, "/opt/iron"); err != nil || state != Already {
		t.Errorf("segunda vez deveria ser Already: %v %v", state, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("o arquivo mudou na segunda execução")
	}

	// outro caminho: atualiza em vez de acrescentar um segundo hook.
	if state, err := InstallAntigravity(dir, "/novo/iron"); err != nil || state != Updated {
		t.Errorf("caminho novo deveria ser Updated: %v %v", state, err)
	}
	found, _ := FindAntigravity(dir)
	if len(found) != 1 || found[0].Exe != "/novo/iron" {
		t.Errorf("deveria haver um só hook, com o caminho novo: %+v", found)
	}

	// prazo apagado na mão: o instalador o repõe.
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), `"timeout": 600`, `"x": 1`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, _ := InstallAntigravity(dir, "/novo/iron"); state != Updated {
		t.Errorf("prazo ausente deveria ser corrigido: %v", state)
	}
}

func TestInstallAntigravityKeepsOtherSets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".agents/hooks.json"),
		`{"meu-linter":{"PostToolUse":[{"matcher":"run_command","hooks":[{"command":"./lint.sh","timeout":10}]}]},"outro":{"enabled":false,"PreToolUse":[]}}`)
	if _, err := InstallAntigravity(dir, "/opt/iron"); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, filepath.Join(dir, ".agents/hooks.json"))
	if len(doc) != 3 || doc["meu-linter"] == nil || doc["outro"].(map[string]any)["enabled"] != false {
		t.Errorf("o conteúdo da pessoa deveria ser mantido: %v", doc)
	}
}

func TestInstallAntigravityBrokenFileIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".agents/hooks.json")
	writeFile(t, path, `{ "probe": `)
	_, err := InstallAntigravity(dir, "/opt/iron")
	if err == nil || !strings.Contains(err.Error(), "hooks.json") {
		t.Errorf("deveria falhar citando o arquivo: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != `{ "probe": ` {
		t.Error("o arquivo da pessoa não pode ser sobrescrito")
	}
}

func TestFindAntigravity(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindAntigravity(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("sem arquivo o erro deveria ser ErrNotExist: %v", err)
	}

	writeFile(t, filepath.Join(dir, ".agents/hooks.json"), `{"a":{"enabled":false,"PreToolUse":[{"matcher":"*","hooks":[{"command":"'/x/iron' hook --agent=antigravity"}]}]},
	  "b":{"PreToolUse":[{"matcher":"run_command","hooks":[{"command":"/bin/sh hook --agent=antigravity"},{"command":"'/y/iron' hook --agent=claude"}]}]}}`)
	found, err := FindAntigravity(dir)
	if err != nil || len(found) != 1 {
		t.Fatalf("só o hook do Iron Brake com o programa iron conta: %+v %v", found, err)
	}
	if f := found[0]; f.Set != "a" || f.Exe != "/x/iron" || f.Matcher != "*" || f.Timeout != 0 || f.Enabled {
		t.Errorf("campos inesperados: %+v", f)
	}

	writeFile(t, filepath.Join(dir, ".agents/hooks.json"), `{`)
	if _, err := FindAntigravity(dir); err == nil {
		t.Error("JSON inválido deveria ser erro")
	}
}

func TestMatcherCovers(t *testing.T) {
	cases := map[string]bool{
		"": true, "*": true, "run_command": true, "run_command|view_file": true, "run_.*": true,
		"view_file": false, "run": false, "browser_.*": false, "(": false,
	}
	for matcher, want := range cases {
		if got := MatcherCovers(matcher, "run_command"); got != want {
			t.Errorf("MatcherCovers(%q) = %v, quero %v", matcher, got, want)
		}
	}
}
