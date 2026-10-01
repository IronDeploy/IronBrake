package hook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSampleEvents(t *testing.T) {
	cases := []struct {
		file     string
		toolName string
		command  string
	}{
		{"git-status.json", "Bash", "git status"},
		{"git-push-force.json", "Bash", "git push --force origin main"},
		{"terraform-apply.json", "Bash", "terraform apply"},
		{"kubectl-delete-namespace.json", "Bash", "kubectl delete namespace prod"},
		// Ferramenta que não é Bash: não tem "command", então o comando fica vazio.
		{"read-tool.json", "Read", ""},
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			path := filepath.Join("..", "..", "testdata", "events", c.file)
			file, err := os.Open(path)
			if err != nil {
				t.Fatalf("erro lendo %s: %v", path, err)
			}
			defer file.Close()

			event, err := Claude.ParseEvent(file)
			if err != nil {
				t.Fatalf("erro decodificando JSON: %v", err)
			}

			if event.Tool != c.toolName {
				t.Errorf("Tool: esperava %q, obtive %q", c.toolName, event.Tool)
			}
			if event.Command != c.command {
				t.Errorf("Command: esperava %q, obtive %q", c.command, event.Command)
			}
			if event.Shell != (c.toolName == "Bash") {
				t.Errorf("Shell: obtive %v para a ferramenta %q", event.Shell, c.toolName)
			}
		})
	}
}
