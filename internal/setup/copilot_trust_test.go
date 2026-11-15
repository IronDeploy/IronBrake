package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCopilotConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// O config.json real começa com linhas "//" (não é JSON estrito).
func TestReadCopilotTrust(t *testing.T) {
	header := "// User settings belong in settings.json.\n// This file is managed automatically.\n"
	cases := []struct {
		name, body string
		want       bool
	}{
		{"pasta na lista", header + `{"trustedFolders":["/a/outra","/work/proj"]}`, true},
		{"barra no fim e ponto", header + `{"trustedFolders":["/work/proj/"]}`, true},
		{"outra pasta", header + `{"trustedFolders":["/work/outro"]}`, false},
		// filha de uma pasta confiada: herança não verificada, então não conta.
		{"só a pasta pai", header + `{"trustedFolders":["/work"]}`, false},
		{"sem a chave", header + `{"loggedInUsers":[]}`, false},
		{"// dentro de um texto não é comentário", header + `{"host":"https://github.com","trustedFolders":["/work/proj"]}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			trust, err := ReadCopilotTrust(writeCopilotConfig(t, c.body), "/work/proj")
			if err != nil || trust.FolderTrusted != c.want {
				t.Errorf("FolderTrusted = %v, erro %v; esperava %v", trust.FolderTrusted, err, c.want)
			}
		})
	}
}

func TestReadCopilotTrustErrors(t *testing.T) {
	// sem o arquivo: nada confiado, sem erro.
	if trust, err := ReadCopilotTrust(filepath.Join(t.TempDir(), "nao-existe.json"), "/work"); err != nil || trust.FolderTrusted {
		t.Errorf("sem arquivo: %+v %v", trust, err)
	}
	for name, body := range map[string]string{
		"JSON quebrado":    "{",
		"lista de números": `{"trustedFolders":[1,2]}`,
		"não é uma lista":  `{"trustedFolders":"/work"}`,
	} {
		path := writeCopilotConfig(t, body)
		if _, err := ReadCopilotTrust(path, "/work"); err == nil || !strings.Contains(err.Error(), "config.json") {
			t.Errorf("%s: deveria ser erro citando o arquivo, obtive %v", name, err)
		}
	}
}

func TestCopilotConfigPath(t *testing.T) {
	t.Setenv("COPILOT_HOME", "/x/copilot-home")
	if got := CopilotConfigPath("/home/u"); got != filepath.Join("/x/copilot-home", "config.json") {
		t.Errorf("COPILOT_HOME deveria valer: %q", got)
	}
	t.Setenv("COPILOT_HOME", "")
	if got := CopilotConfigPath("/home/u"); got != filepath.Join("/home/u", ".copilot", "config.json") {
		t.Errorf("sem COPILOT_HOME vale ~/.copilot: %q", got)
	}
	if got := CopilotConfigPath(""); got != "" {
		t.Errorf("sem home não há caminho: %q", got)
	}
}
