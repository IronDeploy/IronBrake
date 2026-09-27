package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testExe = "/opt/my tools/iron"

const existingSettings = `{
  "model": "opus",
  "env": {"API_URL": "https://example.com"},
  "permissions": {"allow": ["Bash(git status)"]},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Write", "hooks": [{"type": "command", "command": "cd x && ./fmt.sh <in", "timeout": 5}]}
    ],
    "PostToolUse": [
      {"matcher": "", "hooks": [{"type": "command", "command": "echo done"}]}
    ]
  }
}`

func newSettingsPath(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("erro lendo %s: %v", path, err)
	}

	return data
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("JSON inválido: %v\n%s", err, data)
	}

	return out
}

func wantIronGroup() map[string]any {
	return map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{"type": "command", "command": testExe, "args": []any{"hook"}},
		},
	}
}

func TestInstallHookCreatesFile(t *testing.T) {
	path := newSettingsPath(t, "")

	changed, err := InstallHook(path, testExe)

	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !changed {
		t.Error("esperava changed=true ao criar o arquivo")
	}

	want := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{wantIronGroup()},
		},
	}
	if got := decode(t, readFile(t, path)); !reflect.DeepEqual(got, want) {
		t.Errorf("conteúdo diferente do esperado:\nobtive:   %v\nesperava: %v", got, want)
	}
}

func TestInstallHookKeepsExistingSettings(t *testing.T) {
	path := newSettingsPath(t, existingSettings)

	changed, err := InstallHook(path, testExe)

	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !changed {
		t.Error("esperava changed=true ao adicionar o hook")
	}

	// Esperado = o arquivo original + o nosso grupo no fim de PreToolUse.
	want := decode(t, []byte(existingSettings))
	hooks := want["hooks"].(map[string]any)
	hooks["PreToolUse"] = append(hooks["PreToolUse"].([]any), wantIronGroup())

	data := readFile(t, path)
	if got := decode(t, data); !reflect.DeepEqual(got, want) {
		t.Errorf("algo do arquivo original mudou ou sumiu:\nobtive:   %v\nesperava: %v", got, want)
	}
	if !strings.Contains(string(data), "cd x && ./fmt.sh <in") {
		t.Errorf("o texto do comando existente foi reescrito:\n%s", data)
	}
}

func TestInstallHookTwiceDoesNotDuplicate(t *testing.T) {
	cases := []struct {
		name    string
		initial string
	}{
		{"arquivo novo", ""},
		{"arquivo existente", existingSettings},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := newSettingsPath(t, c.initial)

			if _, err := InstallHook(path, testExe); err != nil {
				t.Fatalf("primeira execução: %v", err)
			}
			afterFirst := readFile(t, path)

			changed, err := InstallHook(path, testExe)

			if err != nil {
				t.Fatalf("segunda execução: %v", err)
			}
			if changed {
				t.Error("segunda execução deveria devolver changed=false")
			}
			if afterSecond := readFile(t, path); !bytes.Equal(afterFirst, afterSecond) {
				t.Errorf("segunda execução mudou o arquivo:\n%s", afterSecond)
			}
		})
	}
}

func TestInstallHookRefusesUnexpectedFiles(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"JSON cortado", `{"model": `},
		{"comentário", "{\n// comentário\n}"},
		{"vírgula sobrando", `{"env": {"TOKEN": "s3cr3t-value"},}`},
		{"null", `null`},
		{"lista no lugar do objeto", `[]`},
		{"hooks não é objeto", `{"hooks": []}`},
		{"PreToolUse não é lista", `{"hooks": {"PreToolUse": {}}}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := newSettingsPath(t, c.content)

			changed, err := InstallHook(path, testExe)

			if err == nil {
				t.Fatal("esperava um erro")
			}
			if changed {
				t.Error("changed deveria ser false quando há erro")
			}
			if got := readFile(t, path); string(got) != c.content {
				t.Errorf("o arquivo foi alterado:\n%s", got)
			}
			if strings.Contains(err.Error(), "s3cr3t-value") {
				t.Errorf("o erro vazou o conteúdo do arquivo: %v", err)
			}
		})
	}
}
