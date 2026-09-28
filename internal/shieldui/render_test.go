package shieldui

import (
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/scan"
	"github.com/IronDeploy/IronBrake/internal/shield"
)

func TestRenderEmptyRows(t *testing.T) {
	out := Render(nil, -1)
	if !strings.Contains(out, "Nenhuma credencial") {
		t.Errorf("esperava mensagem de nada encontrado: %q", out)
	}
}

func TestRenderNeverPrintsSecretValues(t *testing.T) {
	secret := "SUPER-FAKE-SECRET-999"
	rows := []Row{{
		Category: shield.Category{Name: "AWS", Rules: []string{"Read(~/.aws/**)"}},
		Findings: []scan.Finding{{Source: "AWS", Where: "~/.aws/credentials [perfil default]", Note: secret, Severity: scan.Critical}},
	}}
	out := Render(rows, 0)
	if strings.Contains(out, secret) {
		t.Errorf("Render nunca deveria imprimir o campo Note/valor: %q", out)
	}
}

func TestRenderShowsStatusAndCategory(t *testing.T) {
	rows := []Row{
		{Category: shield.Category{Name: "AWS", Rules: []string{"Read(~/.aws/**)"}}, Hidden: true,
			Findings: []scan.Finding{{Source: "AWS", Severity: scan.Critical}}},
		{Category: shield.Category{Name: "SSH", Rules: []string{"Read(~/.ssh/**)"}}, Hidden: false,
			Findings: []scan.Finding{{Source: "SSH", Severity: scan.Medium}}},
	}
	out := Render(rows, 1)

	if !strings.Contains(out, "AWS") || !strings.Contains(out, "SSH") {
		t.Errorf("esperava as duas categorias na tela: %q", out)
	}
	if !strings.Contains(out, "OCULTA") {
		t.Errorf("linha travada deveria dizer OCULTA: %q", out)
	}
	if !strings.Contains(out, "VISÍVEL") {
		t.Errorf("linha destravada deveria dizer VISÍVEL: %q", out)
	}
	if !strings.Contains(out, "1/2") {
		t.Errorf("esperava o resumo 1/2 ocultas: %q", out)
	}
}

func TestRenderNonHidableExplainsWhy(t *testing.T) {
	rows := []Row{{
		Category: shield.Category{Name: "variável de ambiente", Why: "motivo de teste"},
		Findings: []scan.Finding{{Source: "variável de ambiente", Severity: scan.Medium}},
	}}
	out := Render(rows, 0)
	if !strings.Contains(out, "motivo de teste") {
		t.Errorf("linha selecionada e não-ocultável deveria mostrar o Why: %q", out)
	}
	if !strings.Contains(out, "N/D") {
		t.Errorf("esperava status N/D para categoria não-ocultável: %q", out)
	}
}
