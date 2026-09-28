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

func TestUnhardenWithoutPriorHardenIsANoop(t *testing.T) {
	// "iron shield unlock" sem nunca ter travado (nem "iron init --harden"
	// ter rodado): nada a fazer, sem erro, sem criar arquivo.
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")

	removed, err := Unharden(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("esperava nada para remover, obtive %v", removed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("unlock sem harden anterior não deveria criar o arquivo")
	}
}

func TestHardenThenUnhardenRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	added, err := Harden(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != len(HardenDenyRules) {
		t.Fatalf("esperava travar %d regras, travou %d", len(HardenDenyRules), len(added))
	}

	removed, err := Unharden(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != len(HardenDenyRules) {
		t.Errorf("esperava destravar %d regras, destravou %d", len(HardenDenyRules), len(removed))
	}

	deny := readDeny(t, path)
	for _, rule := range HardenDenyRules {
		if slices.Contains(deny, rule) {
			t.Errorf("regra %q deveria ter sido removida", rule)
		}
	}
}

func TestUnhardenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Harden(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Unharden(path); err != nil {
		t.Fatal(err)
	}
	removed, err := Unharden(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("segunda passada deveria não remover nada, removeu %d", len(removed))
	}
}

func TestUnhardenPreservesOtherDenyRulesAndContent(t *testing.T) {
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

	if _, err := Unharden(path); err != nil {
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
		t.Fatalf("JSON inválido após unharden: %v", err)
	}
	if doc.Model != "opus" {
		t.Errorf("perdeu a chave model: %q", doc.Model)
	}
	if !slices.Contains(doc.Permissions.Allow, "Bash(go test:*)") {
		t.Error("perdeu a regra allow existente")
	}
	if !slices.Contains(doc.Permissions.Deny, "Read(./segredo.txt)") {
		t.Error("unlock removeu uma regra deny que não era do Iron Shield")
	}
	if slices.Contains(doc.Permissions.Deny, "Read(~/.aws/**)") {
		t.Error("unlock deveria ter removido a regra do Iron Shield")
	}
}

func TestStatusWithoutSettingsIsAllUnlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")

	st, err := Status(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Locked) != 0 {
		t.Errorf("sem settings, nada deveria estar travado, obtive %v", st.Locked)
	}
	if len(st.Unlocked) != len(HardenDenyRules) {
		t.Errorf("esperava %d regras destravadas, obtive %d", len(HardenDenyRules), len(st.Unlocked))
	}
}

func TestStatusAfterHardenIsAllLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Harden(path); err != nil {
		t.Fatal(err)
	}

	st, err := Status(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Unlocked) != 0 {
		t.Errorf("depois do harden, nada deveria estar destravado, obtive %v", st.Unlocked)
	}
	if len(st.Locked) != len(HardenDenyRules) {
		t.Errorf("esperava %d regras travadas, obtive %d", len(HardenDenyRules), len(st.Locked))
	}
}

func TestStatusPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := `{"permissions": {"deny": ["Read(~/.aws/**)", "Read(~/.ssh/**)"]}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Status(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Locked) != 2 {
		t.Errorf("esperava 2 regras travadas, obtive %d: %v", len(st.Locked), st.Locked)
	}
	if len(st.Unlocked) != len(HardenDenyRules)-2 {
		t.Errorf("esperava %d regras destravadas, obtive %d", len(HardenDenyRules)-2, len(st.Unlocked))
	}
}
