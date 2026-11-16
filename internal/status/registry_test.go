package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/agents.json é o registro que os scripts de verificação e o workflow
// agendado leem; Verified é o que o iron status mostra. Os dois têm de dizer a
// mesma coisa, ou a rotina de reteste compara com uma versão que a tela não mostra.
func TestVerifiedMatchesTheAgentRegistry(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry map[string]struct {
		Display  string `json:"display"`
		Verified string `json:"verified"`
		Latest   map[string]string
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatal(err)
	}

	for _, name := range Order {
		entry, ok := registry[name]
		if !ok {
			t.Errorf("%s está no iron status e falta em scripts/agents.json", name)
			continue
		}
		if entry.Verified == "" || len(entry.Latest) != 1 {
			t.Errorf("%s: o registro precisa de \"verified\" e de uma fonte em \"latest\": %+v", name, entry)
		}
		if !strings.Contains(Verified[name], entry.Verified) {
			t.Errorf("%s: scripts/agents.json diz %q e o iron status diz %q", name, entry.Verified, Verified[name])
		}
	}
	for name := range registry {
		if _, ok := Verified[name]; !ok {
			t.Errorf("%s está em scripts/agents.json e falta no iron status", name)
		}
	}
}
