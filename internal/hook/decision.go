package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type Decision string

const (
	Allow Decision = "allow" // sem opinião: as regras do Claude Code decidem
	Ask   Decision = "ask"
	Deny  Decision = "deny"

	// UserApproved: aprovado na janela do Iron Brake. Vira um allow explícito.
	UserApproved Decision = "userApproved"
)

// Códigos de saída que o Claude Code entende. Nunca usar 1: ele NÃO bloqueia.
const (
	exitOK    = 0
	exitBlock = 2
)

// Deadline é o tempo máximo do hook: acima do pior caso normal (janela 480 s
// + terraform show 30 s) e abaixo dos 600 s após os quais o Claude Code
// libera o comando. Estourado, o hook responde deny.
const Deadline = 560 * time.Second

// MinTimeout é o menor "timeout" aceitável no settings.json: o deny por
// Deadline precisa chegar antes de o Claude Code desistir.
const MinTimeout = Deadline + 10*time.Second

// hookOutput é o JSON do PreToolUse. SystemMessage repete o motivo porque, na
// extensão do VS Code, o permissionDecisionReason não aparece na tela.
type hookOutput struct {
	HookSpecificOutput decisionDetails `json:"hookSpecificOutput"`
	SystemMessage      string          `json:"systemMessage,omitempty"`
}

type decisionDetails struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// Respond escreve a resposta e devolve o código de saída. Qualquer falha ou
// decisão desconhecida bloqueia.
func Respond(stdout, stderr io.Writer, decision Decision, reason string) int {
	switch decision {
	case Allow:
		return exitOK
	case Ask:
		out := hookOutput{
			HookSpecificOutput: decisionDetails{
				HookEventName:            "PreToolUse",
				PermissionDecision:       string(Ask),
				PermissionDecisionReason: reason,
			},
			SystemMessage: reason,
		}
		if err := json.NewEncoder(stdout).Encode(out); err != nil {
			fmt.Fprintln(stderr, "iron: não consegui escrever a resposta ask")
			return exitBlock
		}
		return exitOK
	case UserApproved:
		out := hookOutput{
			HookSpecificOutput: decisionDetails{
				HookEventName:            "PreToolUse",
				PermissionDecision:       string(Allow),
				PermissionDecisionReason: reason,
			},
		}
		if err := json.NewEncoder(stdout).Encode(out); err != nil {
			fmt.Fprintln(stderr, "iron: não consegui escrever a resposta allow")
			return exitBlock
		}
		return exitOK
	case Deny:
		fmt.Fprintln(stderr, reason)
		return exitBlock
	}

	fmt.Fprintf(stderr, "iron: decisão desconhecida %q\n", decision)
	return exitBlock
}
