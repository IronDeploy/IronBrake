package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Codex é o Codex CLI (OpenAI). Contrato verificado com o agente real (0.159.3),
// em doc/agents.md: só um deny reconhecido bloqueia (exit 2 com stderr, ou JSON
// hookSpecificOutput.permissionDecision "deny"). Tudo o mais LIBERA o comando:
// exit 1, "ask" e "allow" em JSON ("unsupported permissionDecision"), stdout
// inválido, {} e prazo estourado.
var Codex Agent = codexAgent{}

type codexAgent struct{}

func (codexAgent) Name() string { return "codex" }

// CodexShellTool é a ferramenta de shell, e o matcher (regex) do hooks.json.
// O apply_patch também chega com o texto do patch em tool_input.command e NÃO
// é shell.
const (
	CodexShellTool    = "Bash"
	CodexShellMatcher = "^Bash$"
)

// CodexTimeout é o prazo gravado no hooks.json (o padrão do Codex também é
// 600 s). A janela espera no máximo CodexDialogCap: no "codex exec" uma chamada
// com hook acima de ~55 s é abandonada e refeita (medido), o que abriria uma
// segunda janela. No chat interativo o Codex espera hooks longos.
const (
	CodexTimeout   = 600 * time.Second
	CodexDialogCap = 45 * time.Second
)

// CanAsk é falso: o Codex trata "ask" como falha do hook e executa o comando.
// AppID: o "iron init" grava a etiqueta em shell_environment_policy do
// .codex/config.toml do projeto (verificado no exec e no chat).
func (codexAgent) Capabilities() Capabilities {
	return Capabilities{CanAsk: false, Timeout: CodexTimeout, DialogCap: CodexDialogCap, AppID: "iron-codex"}
}

// codexEvent é o stdin do PreToolUse (só os campos usados).
type codexEvent struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

func (codexAgent) ParseEvent(stdin io.Reader) (Event, error) {
	var in codexEvent
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return Event{}, err
	}
	return Event{
		Command:   in.ToolInput.Command,
		Tool:      in.ToolName,
		Shell:     in.ToolName == CodexShellTool,
		SessionID: in.SessionID,
		Cwd:       in.Cwd,
	}, nil
}

// Respond: sem saída para deixar passar (um JSON "allow" conta como falha do
// hook). Para bloquear usa o exit 2 com o motivo no stderr, que o Codex mostra
// como "Blocked by hook" e entrega ao modelo.
func (codexAgent) Respond(stdout, stderr io.Writer, decision Decision, reason string) int {
	switch decision {
	case Allow, UserApproved:
		return exitOK
	case Deny, Ask:
		fmt.Fprintln(stderr, reason)
		return exitBlock
	}
	fmt.Fprintf(stderr, "iron: decisão desconhecida %q\n", decision)
	return exitBlock
}
