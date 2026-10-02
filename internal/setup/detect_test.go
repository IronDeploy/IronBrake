package setup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func noPath(string) (string, error) { return "", errors.New("não achei") }

func TestDetectAgentsByHomeFolders(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".claude", ".kiro", ".gemini/antigravity-cli", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := DetectAgents(home, noPath)
	want := []string{"claude", "kiro", "antigravity", "codex"}
	if len(got) != len(want) {
		t.Fatalf("esperava %v, obtive %+v", want, got)
	}
	for i, d := range got {
		if d.Agent != want[i] || d.Evidence == "" {
			t.Errorf("posição %d: %+v (quero %s, com evidência)", i, d, want[i])
		}
	}
}

func TestDetectAgentsGeminiFolderAloneIsNotAntigravity(t *testing.T) {
	home := t.TempDir()
	// o Gemini CLI antigo também criava ~/.gemini
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectAgents(home, noPath); len(got) != 0 {
		t.Errorf("~/.gemini sem antigravity-cli não conta: %+v", got)
	}
}

func TestDetectAgentsByProgramOnPath(t *testing.T) {
	look := func(name string) (string, error) {
		if name == "agy" || name == "codex" {
			return "/opt/homebrew/bin/" + name, nil
		}
		return "", errors.New("não achei")
	}
	got := DetectAgents(t.TempDir(), look)
	if len(got) != 2 || got[0].Agent != "antigravity" || got[1].Agent != "codex" || got[0].Evidence != "agy em /opt/homebrew/bin/agy" {
		t.Errorf("deveria achar pelo PATH: %+v", got)
	}
}

func TestDetectAgentsCodexHome(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("CODEX_HOME", custom)
	got := DetectAgents(t.TempDir(), noPath)
	if len(got) != 1 || got[0].Agent != "codex" || got[0].Evidence != custom {
		t.Errorf("CODEX_HOME deveria valer: %+v", got)
	}
}

func TestDetectAgentsNothing(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	if got := DetectAgents(t.TempDir(), noPath); len(got) != 0 {
		t.Errorf("nada instalado: %+v", got)
	}
}
