package shieldui

import (
	"fmt"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/scan"
)

func severityIcon(s scan.Severity) string {
	switch s {
	case scan.Critical:
		return "🔴"
	case scan.High:
		return "🟠"
	case scan.Medium:
		return "🟡"
	default:
		return "⚪"
	}
}

// statusLabel é o que importa nesta tela: não a gravidade (isso é o scan),
// mas se o agente enxerga a credencial agora ou não.
func statusLabel(r Row) string {
	switch {
	case !r.Hidable():
		return "N/D"
	case r.Hidden:
		return "⛔ OCULTA "
	default:
		return "✅ VISÍVEL"
	}
}

// reverseVideo destaca a linha selecionada — funciona em qualquer terminal
// ANSI, sem precisar saber a cor de fundo dele.
func reverseVideo(s string) string { return "\x1b[7m" + s + "\x1b[27m" }

// Render desenha a tela inteira como uma string pronta para escrever de uma
// vez (sem flicker de escrever linha por linha). cursor é o índice da linha
// selecionada em rows; -1 = nenhuma linha (lista vazia).
func Render(rows []Row, cursor int) string {
	var b strings.Builder

	b.WriteString("Iron Shield — gerenciar credenciais (setas: navega · enter: oculta/mostra · q/esc: sai)\n\n")

	if len(rows) == 0 {
		b.WriteString("Nenhuma credencial de risco encontrada ao alcance nesta máquina agora.\n")
		return b.String()
	}

	for i, r := range rows {
		line := fmt.Sprintf("%s  %-10s  %s  %-20s  %d achado(s)",
			severityIcon(r.Worst()), statusLabel(r), bullet(i == cursor), r.Category.Name, len(r.Findings))
		if i == cursor {
			line = reverseVideo(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
		if i == cursor && !r.Hidable() {
			fmt.Fprintf(&b, "      não dá para ocultar por aqui: %s\n", r.Category.Why)
		}
	}

	b.WriteString("\n")
	locked, total := 0, 0
	for _, r := range rows {
		if r.Hidable() {
			total++
			if r.Hidden {
				locked++
			}
		}
	}
	fmt.Fprintf(&b, "%d/%d categoria(s) ocultáveis estão ocultas agora. Nenhum valor de segredo aparece nesta tela.\n", locked, total)
	return b.String()
}

func bullet(selected bool) string {
	if selected {
		return "▶"
	}
	return " "
}
