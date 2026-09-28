package shieldui

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/scan"
)

func TestBuildRowsOnlyIncludesCategoriesWithFindings(t *testing.T) {
	findings := []scan.Finding{
		{Source: "AWS", Where: "~/.aws/credentials [perfil default]", Severity: scan.Critical},
		{Source: "SSH", Where: "~/.ssh/id_ed25519", Severity: scan.Medium},
	}

	rows := BuildRows(findings, func([]string) bool { return false })

	if len(rows) != 2 {
		t.Fatalf("esperava 2 linhas (AWS, SSH), obtive %d", len(rows))
	}
	names := map[string]bool{}
	for _, r := range rows {
		names[r.Category.Name] = true
	}
	if !names["AWS"] || !names["SSH"] {
		t.Errorf("esperava AWS e SSH nas linhas, obtive %+v", rows)
	}
}

func TestBuildRowsPreservesCategoryOrder(t *testing.T) {
	// SSH aparece antes de AWS na fixture, mas shield.Categories põe AWS
	// primeiro — a tela deve seguir a ordem da tabela, não a do scan.
	findings := []scan.Finding{
		{Source: "SSH", Where: "x", Severity: scan.Medium},
		{Source: "AWS", Where: "y", Severity: scan.Critical},
	}

	rows := BuildRows(findings, func([]string) bool { return false })

	if len(rows) != 2 || rows[0].Category.Name != "AWS" || rows[1].Category.Name != "SSH" {
		t.Fatalf("esperava [AWS, SSH] na ordem de shield.Categories, obtive %+v", rows)
	}
}

func TestBuildRowsGroupsMultipleFindingsPerCategory(t *testing.T) {
	findings := []scan.Finding{
		{Source: "AWS", Where: "perfil a", Severity: scan.Medium},
		{Source: "AWS", Where: "perfil b", Severity: scan.Critical},
	}

	rows := BuildRows(findings, func([]string) bool { return false })

	if len(rows) != 1 {
		t.Fatalf("esperava 1 linha para AWS, obtive %d", len(rows))
	}
	if len(rows[0].Findings) != 2 {
		t.Errorf("esperava 2 achados agrupados, obtive %d", len(rows[0].Findings))
	}
	if rows[0].Worst() != scan.Critical {
		t.Errorf("Worst() deveria ser Critical, obtive %v", rows[0].Worst())
	}
}

func TestBuildRowsSetsHidden(t *testing.T) {
	findings := []scan.Finding{{Source: "AWS", Where: "x", Severity: scan.Critical}}

	locked := BuildRows(findings, func(rules []string) bool { return true })
	if !locked[0].Hidden {
		t.Error("esperava Hidden=true quando locked() devolve true")
	}

	unlocked := BuildRows(findings, func(rules []string) bool { return false })
	if unlocked[0].Hidden {
		t.Error("esperava Hidden=false quando locked() devolve false")
	}
}

func TestBuildRowsNotHidableIgnoresLockedFunc(t *testing.T) {
	// "variável de ambiente" não tem Rules — nunca deve aparecer como Hidden,
	// mesmo que locked() devolva true (não faria sentido ser chamado com
	// rules vazio, mas a linha não pode alegar estar oculta de qualquer jeito).
	findings := []scan.Finding{{Source: "variável de ambiente", Where: "MY_SECRET", Severity: scan.Medium}}

	rows := BuildRows(findings, func([]string) bool { return true })

	if len(rows) != 1 {
		t.Fatalf("esperava 1 linha, obtive %d", len(rows))
	}
	if rows[0].Hidden {
		t.Error("categoria não-ocultável nunca deveria marcar Hidden=true")
	}
	if rows[0].Hidable() {
		t.Error("variável de ambiente não deveria ser Hidable()")
	}
}

func TestBuildRowsUnknownSourceIsSkipped(t *testing.T) {
	// Não deveria acontecer em produção (há um teste de alinhamento em
	// internal/scan para isso), mas a tela não pode quebrar se acontecer.
	findings := []scan.Finding{{Source: "fonte-nova-desconhecida", Where: "x", Severity: scan.Low}}

	rows := BuildRows(findings, func([]string) bool { return false })

	if len(rows) != 0 {
		t.Errorf("fonte desconhecida não deveria virar linha, obtive %+v", rows)
	}
}
