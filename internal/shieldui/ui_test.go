package shieldui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/scan"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/shield"
)

// TestRunWithNoFindingsJustPrintsAndReturns cobre o pedido: sem nada achado,
// "iron scan --manage" não deve travar esperando tecla nem exigir um tty de
// verdade — só imprime a mensagem e sai.
func TestRunWithNoFindingsJustPrintsAndReturns(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer inW.Close() // nunca escrita: Run não pode tentar ler daqui

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outR.Close()

	done := make(chan error, 1)
	go func() { done <- Run(settingsPath, nil, inR, outW, nil) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("esperava sair sem erro, obtive: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() travou esperando tecla mesmo sem achados")
	}
	outW.Close()

	out, _ := io.ReadAll(outR)
	if !strings.Contains(string(out), "Nenhuma credencial") {
		t.Errorf("esperava a mensagem de nada encontrado: %q", out)
	}
}

func TestLoadRowsReflectsExistingLock(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	awsCat, _ := shield.For("AWS")
	if _, err := setup.HardenRules(settingsPath, awsCat.Rules); err != nil {
		t.Fatal(err)
	}

	findings := []scan.Finding{
		{Source: "AWS", Where: "x", Severity: scan.Critical},
		{Source: "SSH", Where: "y", Severity: scan.Medium},
	}
	rows, err := loadRows(settingsPath, findings)
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 {
		t.Fatalf("esperava 2 linhas, obtive %d", len(rows))
	}
	for _, r := range rows {
		switch r.Category.Name {
		case "AWS":
			if !r.Hidden {
				t.Error("AWS deveria estar Hidden=true (foi travada antes)")
			}
		case "SSH":
			if r.Hidden {
				t.Error("SSH deveria estar Hidden=false (nunca foi travada)")
			}
		}
	}
}

func TestLoadRowsWithoutSettingsIsAllVisible(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), ".claude", "settings.json")
	findings := []scan.Finding{{Source: "AWS", Where: "x", Severity: scan.Critical}}

	rows, err := loadRows(settingsPath, findings)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Hidden {
		t.Errorf("sem settings, esperava 1 linha visível, obtive %+v", rows)
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("loadRows não deveria criar o settings.json")
	}
}

func TestToggleLocksThenUnlocksAndCallsAudit(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	awsCat, _ := shield.For("AWS")
	row := Row{Category: awsCat, Hidden: false}

	var events []string
	audit := func(action, category string, n int) {
		events = append(events, action+":"+category)
		if n == 0 {
			t.Errorf("esperava contagem de regras > 0 no evento %s:%s", action, category)
		}
	}

	row, err := toggle(settingsPath, row, audit)
	if err != nil {
		t.Fatal(err)
	}
	if !row.Hidden {
		t.Error("depois do primeiro toggle, esperava Hidden=true")
	}
	data, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(data), "Read(~/.aws/**)") {
		t.Errorf("esperava a regra AWS gravada:\n%s", data)
	}

	row, err = toggle(settingsPath, row, audit)
	if err != nil {
		t.Fatal(err)
	}
	if row.Hidden {
		t.Error("depois do segundo toggle, esperava Hidden=false")
	}
	data, _ = os.ReadFile(settingsPath)
	if strings.Contains(string(data), "Read(~/.aws/**)") {
		t.Errorf("esperava a regra AWS removida:\n%s", data)
	}

	if len(events) != 2 || events[0] != "lock:AWS" || events[1] != "unlock:AWS" {
		t.Errorf("esperava eventos [lock:AWS unlock:AWS], obtive %v", events)
	}
}

func TestToggleNonHidableIsANoop(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	envCat, _ := shield.For("variável de ambiente")
	row := Row{Category: envCat, Hidden: false}

	auditCalled := false
	got, err := toggle(settingsPath, row, func(string, string, int) { auditCalled = true })
	if err != nil {
		t.Fatal(err)
	}
	if got.Hidden {
		t.Error("categoria não-ocultável não deveria virar Hidden")
	}
	if auditCalled {
		t.Error("não deveria auditar uma categoria que não foi realmente alterada")
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("toggle não-ocultável não deveria criar o settings.json")
	}
}
