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

func TestSetEnv(t *testing.T) {
	read := func(t *testing.T, path string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("o arquivo ficou inválido: %v\n%s", err, data)
		}
		return doc
	}

	t.Run("arquivo novo", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".claude", "settings.json")
		changed, current, err := SetEnv(path, "AWS_SDK_UA_APP_ID", "iron-claude")
		if err != nil || !changed || current != "iron-claude" {
			t.Fatalf("%v %q %v", changed, current, err)
		}
		env := read(t, path)["env"].(map[string]any)
		if env["AWS_SDK_UA_APP_ID"] != "iron-claude" {
			t.Errorf("env: %v", env)
		}
	})

	t.Run("preserva o resto do arquivo e as outras variáveis", func(t *testing.T) {
		path := newSettingsPath(t, `{"model":"opus","env":{"OUTRA":"x"},"permissions":{"deny":["Read(~/.ssh/**)"]}}`)
		changed, _, err := SetEnv(path, "AWS_SDK_UA_APP_ID", "iron-claude")
		if err != nil || !changed {
			t.Fatalf("%v %v", changed, err)
		}
		doc := read(t, path)
		env := doc["env"].(map[string]any)
		if env["OUTRA"] != "x" || env["AWS_SDK_UA_APP_ID"] != "iron-claude" || doc["model"] != "opus" || doc["permissions"] == nil {
			t.Errorf("o resto do arquivo mudou: %v", doc)
		}
	})

	t.Run("idempotente", func(t *testing.T) {
		path := newSettingsPath(t, `{}`)
		SetEnv(path, "K", "v")
		first, _ := os.ReadFile(path)
		changed, current, err := SetEnv(path, "K", "v")
		second, _ := os.ReadFile(path)
		if err != nil || changed || current != "v" || string(first) != string(second) {
			t.Errorf("segunda chamada: %v %q %v", changed, current, err)
		}
	})

	t.Run("não sobrescreve valor da pessoa", func(t *testing.T) {
		path := newSettingsPath(t, `{"env":{"AWS_SDK_UA_APP_ID":"meu-app"}}`)
		before, _ := os.ReadFile(path)
		changed, current, err := SetEnv(path, "AWS_SDK_UA_APP_ID", "iron-claude")
		after, _ := os.ReadFile(path)
		if err != nil || changed || current != "meu-app" || string(before) != string(after) {
			t.Errorf("%v %q %v", changed, current, err)
		}
	})

	t.Run("arquivo inválido ou env que não é objeto", func(t *testing.T) {
		for _, content := range []string{`{"env": `, `{"env": 5}`, `[]`, `{"env": []}`} {
			path := newSettingsPath(t, content)
			if _, _, err := SetEnv(path, "K", "v"); err == nil {
				t.Errorf("%q deveria dar erro", content)
			}
			if after, _ := os.ReadFile(path); string(after) != content {
				t.Errorf("arquivo inválido não pode ser alterado: %q", after)
			}
		}
	})
}

func TestInstallHookRecognizesTheSameProgramWithAnUncleanPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")

	if changed, err := InstallHook(path, "/opt/iron/bin/iron"); err != nil || !changed {
		t.Fatalf("1ª instalação: %v %v", changed, err)
	}
	// O mesmo programa por um caminho com "..", ".", "//": não é um segundo hook.
	for _, again := range []string{"/opt/iron/x/../bin/iron", "/opt/iron/./bin/iron", "/opt//iron/bin/iron"} {
		changed, err := InstallHook(path, again)
		if err != nil || changed {
			t.Errorf("%s: não deveria instalar de novo (%v %v)", again, changed, err)
		}
	}

	hooks, err := FindHooks(path)
	if err != nil || len(hooks) != 1 {
		t.Errorf("deveria haver um hook só: %v %v", hooks, err)
	}

	// Outro programa de verdade continua sendo outro hook.
	if changed, _ := InstallHook(path, "/opt/outro/bin/iron"); !changed {
		t.Error("um caminho diferente de verdade deve instalar")
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := writeFileAtomic(path, []byte("novo\n")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Errorf("arquivo novo deve sair 0644: %v", info.Mode().Perm())
	}

	// Mantém a permissão que já existia.
	os.Chmod(path, 0o600)
	if err := writeFileAtomic(path, []byte("outro\n")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("deve manter 0600: %v", info.Mode().Perm())
	}
	if data, _ := os.ReadFile(path); string(data) != "outro\n" {
		t.Errorf("conteúdo: %q", data)
	}

	// Não deixa temporários para trás, nem quando falha.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("sobrou lixo na pasta: %v", entries)
	}
	if err := writeFileAtomic(filepath.Join(dir, "nao-existe", "x.json"), []byte("x")); err == nil {
		t.Error("pasta inexistente deve dar erro")
	}
	if entries, _ = os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("falha não pode deixar temporário: %v", entries)
	}
}

func TestSetEnvKeepsFilePermission(t *testing.T) {
	path := newSettingsPath(t, `{}`)
	os.Chmod(path, 0o600)

	if _, _, err := SetEnv(path, "K", "v"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("a permissão do arquivo da pessoa deve ficar: %v", info.Mode().Perm())
	}
}
