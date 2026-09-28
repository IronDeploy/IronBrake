package scan

import (
	"fmt"
	"strings"
)

// icon mostra a gravidade de bater o olho, no mesmo código de cores da ficha
// de risco (🔴 crítico · 🟠 alto · 🟡 médio · ⚪ baixo).
func icon(s Severity) string {
	switch s {
	case Critical:
		return "🔴"
	case High:
		return "🟠"
	case Medium:
		return "🟡"
	default:
		return "⚪"
	}
}

// Report formata os achados agrupados por gravidade, do mais grave ao menos.
// Só rótulos seguros — nenhum valor de segredo aparece aqui.
func Report(findings []Finding) string {
	var b strings.Builder
	b.WriteString("Iron Shield — raio-X das credenciais ao alcance do agente\n\n")

	if len(findings) == 0 {
		b.WriteString("Nenhuma credencial de risco encontrada ao alcance. ✅\n")
		return b.String()
	}

	current := Severity(-1)
	for _, f := range findings {
		if f.Severity != current {
			current = f.Severity
			fmt.Fprintf(&b, "%s %s\n", icon(f.Severity), strings.ToUpper(f.Severity.String()))
		}
		fmt.Fprintf(&b, "   • %s · %s — %s\n", f.Source, f.Where, f.Note)
	}

	fmt.Fprintf(&b, "\n%d credencial(is) ao alcance. Nenhum valor de segredo foi lido ou impresso.\n", len(findings))
	b.WriteString("Dica: reduza o alcance (credenciais temporárias, perfil só-leitura, tirar segredos de .env).\n")
	return b.String()
}
