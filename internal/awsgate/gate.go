package awsgate

import (
	"regexp"
	"strings"

	"github.com/IronDeploy/IronBrake/internal/policy"
)

// Action é o que o portão faz com um pedido de credenciais.
type Action int

const (
	// Pass: ninguém na cadeia é um agente (um humano no terminal). Entrega sem
	// registrar nada.
	Pass Action = iota
	// Allow: agente em uma conta que não é produção. Entrega e registra.
	Allow
	// Window: agente em produção com a liberação do usuário ainda vigente.
	Window
	// Prompt: agente em produção sem liberação. Pergunta ao usuário.
	Prompt
)

// Decide diz o que fazer. agent vazio é humano.
func Decide(agent string, production, windowValid bool) Action {
	switch {
	case agent == "":
		return Pass
	case !production:
		return Allow
	case windowValid:
		return Window
	default:
		return Prompt
	}
}

// Modos de --env.
const (
	EnvAuto       = "auto"       // pelo nome do perfil: prod, production, prd...
	EnvProduction = "production" // sempre produção
	EnvStrict     = "strict"     // produção, a não ser que o nome seja de teste
	EnvDev        = "dev"        // nunca produção
)

// ValidEnv diz se o modo existe.
func ValidEnv(mode string) bool {
	switch mode {
	case EnvAuto, EnvProduction, EnvStrict, EnvDev:
		return true
	}
	return false
}

// IsProduction diz se o perfil conta como produção. names são o nome dado ao
// perfil (--label) e o AWS_PROFILE do ambiente.
func IsProduction(mode string, names ...string) bool {
	p := policy.Default()
	switch mode {
	case EnvProduction:
		return true
	case EnvDev:
		return false
	case EnvStrict:
		p.AssumeProduction = true
		return p.AssumesProduction(names) || p.IsProduction(names...)
	default:
		return p.IsProduction(names...)
	}
}

var unsafeLabel = regexp.MustCompile(`[^A-Za-z0-9_.:/@+-]`)

const maxLabel = 64

// SanitizeLabel deixa só caracteres comuns de nome de perfil. O AWS_PROFILE vem
// do ambiente, que o agente controla, e o nome entra na janela e no log.
func SanitizeLabel(label string) string {
	label = strings.TrimSpace(label)
	label = unsafeLabel.ReplaceAllString(label, "?")
	if len(label) > maxLabel {
		label = label[:maxLabel] + "…"
	}
	return label
}
