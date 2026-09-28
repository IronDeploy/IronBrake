package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func readDeny(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("lendo %s: %v", path, err)
	}
	var doc struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	return doc.Permissions.Deny
}

func TestHardenCreatesDenyRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")

	added, err := Harden(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != len(HardenDenyRules) {
		t.Errorf("esperava adicionar %d regras, adicionou %d", len(HardenDenyRules), len(added))
	}

	deny := readDeny(t, path)
	for _, rule := range HardenDenyRules {
		if !slices.Contains(deny, rule) {
			t.Errorf("regra ausente no arquivo: %q", rule)
		}
	}
}

func TestHardenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Harden(path); err != nil {
		t.Fatal(err)
	}
	added, err := Harden(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("segunda passada deveria não adicionar nada, adicionou %d", len(added))
	}
}

func TestHardenPreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := `{
  "model": "opus",
  "permissions": {
    "allow": ["Bash(go test:*)"],
    "deny": ["Read(./segredo.txt)"]
  }
}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Harden(path); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var doc struct {
		Model       string `json:"model"`
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("JSON inválido após harden: %v", err)
	}

	if doc.Model != "opus" {
		t.Errorf("perdeu a chave model: %q", doc.Model)
	}
	if !slices.Contains(doc.Permissions.Allow, "Bash(go test:*)") {
		t.Error("perdeu a regra allow existente")
	}
	if !slices.Contains(doc.Permissions.Deny, "Read(./segredo.txt)") {
		t.Error("perdeu a regra deny existente")
	}
	if !slices.Contains(doc.Permissions.Deny, "Read(~/.aws/**)") {
		t.Error("não acrescentou as regras do harden")
	}
}
