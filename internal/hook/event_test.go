package hook

import (
	"encoding/json"
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
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("erro lendo %s: %v", path, err)
			}

			var event PreToolUseEvent
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatalf("erro decodificando JSON: %v", err)
			}

			if event.ToolName != c.toolName {
				t.Errorf("ToolName: esperava %q, obtive %q", c.toolName, event.ToolName)
			}
			if event.ToolInput.Command != c.command {
				t.Errorf("ToolInput.Command: esperava %q, obtive %q", c.command, event.ToolInput.Command)
			}
		})
	}
}
