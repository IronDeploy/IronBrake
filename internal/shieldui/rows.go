// Package shieldui é a tela interativa de "iron scan --manage": mostra, por
// categoria de credencial achada pelo scan, se ela está oculta (regra deny
// travada) ou visível ao agente, e deixa alternar com o teclado.
package shieldui

import (
	"github.com/IronDeploy/IronBrake/internal/scan"
	"github.com/IronDeploy/IronBrake/internal/shield"
)

// Row é uma linha da tela: uma categoria, o que o scan achou dela nesta
// máquina, e se está oculta do agente agora.
type Row struct {
	Category shield.Category
	Findings []scan.Finding
	// Hidden é true só quando TODAS as regras da categoria estão travadas.
	// Falso também cobre o caso parcial (algumas travadas, outras não) — do
	// ponto de vista do agente, se falta uma regra, a fonte ainda é alcançável.
	Hidden bool
}

// Worst devolve a gravidade mais alta entre os achados da linha.
func (r Row) Worst() scan.Severity {
	worst := scan.Low
	for _, f := range r.Findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	return worst
}

// Hidable é falso para categorias como "variável de ambiente" ou "git", que
// o Iron Shield não consegue ocultar via regra deny (ver Category.Why).
func (r Row) Hidable() bool { return len(r.Category.Rules) > 0 }

// BuildRows agrupa os achados do scan por categoria (na ordem de
// shield.Categories) e cruza com quais regras já estão travadas no settings,
// via um lookup locked(rule) devolvido por quem já leu o settings uma vez.
// Só entram categorias com pelo menos um achado — a tela é um raio-X do que
// EXISTE nesta máquina agora, igual ao "iron scan".
func BuildRows(findings []scan.Finding, locked func(rules []string) bool) []Row {
	bySource := map[string][]scan.Finding{}
	for _, f := range findings {
		bySource[f.Source] = append(bySource[f.Source], f)
	}

	var rows []Row
	for _, c := range shield.Categories {
		fs, ok := bySource[c.Name]
		if !ok {
			continue
		}
		rows = append(rows, Row{
			Category: c,
			Findings: fs,
			Hidden:   len(c.Rules) > 0 && locked(c.Rules),
		})
	}
	return rows
}
