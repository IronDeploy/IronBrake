package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Cursor é o Cursor (app e cursor-agent). Contrato da documentação oficial
// (cursor.com/docs/hooks) e do que foi visto no agente real (cursor-agent
// 2026.11.13 e o app 3.30.14): o hook "beforeShellExecution" recebe o comando em
// "command", e "cwd" chega vazio (a pasta vem de workspace_roots). A resposta
// {"permission":"deny"} bloqueia, e {"permission":"allow"} NÃO passa por cima da
// aprovação do próprio Cursor.
//
// O hook só protege com "failClosed": true no hooks.json. Sem ele, exit 1,
// saída vazia e prazo estourado LIBERAM o comando; com ele, bloqueiam. Por isso
// o Iron Brake sempre responde com JSON, inclusive no allow: saída vazia com
// failClosed é bloqueio.
var Cursor Agent = cursorAgent{}

type cursorAgent struct{}

func (cursorAgent) Name() string { return "cursor" }

// CursorEvent é o hook gravado no hooks.json; só ele recebe o comando de shell
// do app e do cursor-agent.
const CursorEvent = "beforeShellExecution"

// CursorShellTool é a ferramenta de shell no payload do preToolUse.
const CursorShellTool = "Shell"

// CursorTimeout é o prazo gravado em "timeout". O padrão do Cursor é 60 s, e o
// hook que o estoura é bloqueado só com failClosed.
const CursorTimeout = 600 * time.Second

// CanAsk é falso: com --force/--yolo o Cursor aprova o "ask" do hook, então ele
// não serve de confirmação. O Iron Brake pergunta na própria janela.
func (cursorAgent) Capabilities() Capabilities {
	return Capabilities{CanAsk: false, Timeout: CursorTimeout, AppID: "iron-cursor"}
}

// cursorEvent é o stdin (só os campos usados). O beforeShellExecution traz
// "command"; o preToolUse traz tool_name e tool_input.command.
type cursorEvent struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd"`

	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	} `json:"tool_input"`

	SessionID      string   `json:"session_id"`
	ConversationID string   `json:"conversation_id"`
	WorkspaceRoots []string `json:"workspace_roots"`
}

func (cursorAgent) ParseEvent(stdin io.Reader) (Event, error) {
	var in cursorEvent
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return Event{}, err
	}
	ev := Event{Tool: in.ToolName, SessionID: in.SessionID, Cwd: in.Cwd}
	if ev.SessionID == "" {
		ev.SessionID = in.ConversationID
	}
	switch {
	case in.ToolName == "":
		// beforeShellExecution: só chega comando de shell.
		ev.Tool, ev.Shell, ev.Command = CursorShellTool, true, in.Command
	case in.ToolName == CursorShellTool:
		ev.Shell, ev.Command = true, in.ToolInput.Command
		if ev.Cwd == "" {
			ev.Cwd = in.ToolInput.Cwd
		}
	}
	// O "cwd" vem vazio no cursor-agent e no app: o comando roda na raiz do projeto.
	if ev.Cwd == "" && len(in.WorkspaceRoots) > 0 {
		ev.Cwd = in.WorkspaceRoots[0]
	}
	return ev, nil
}

type cursorDecision struct {
	Permission   string `json:"permission"`
	UserMessage  string `json:"user_message,omitempty"`
	AgentMessage string `json:"agent_message,omitempty"`
}

func (cursorAgent) Respond(stdout, stderr io.Writer, decision Decision, reason string) int {
	switch decision {
	case Allow, UserApproved:
		return writeCursor(stdout, stderr, cursorDecision{Permission: "allow"}, "")
	case Deny, Ask:
		return writeCursor(stdout, stderr, cursorDecision{Permission: "deny", UserMessage: reason, AgentMessage: reason}, reason)
	}
	msg := fmt.Sprintf("iron: decisão desconhecida %q", decision)
	return writeCursor(stdout, stderr, cursorDecision{Permission: "deny", UserMessage: msg, AgentMessage: msg}, msg)
}

// writeCursor responde em JSON. Se não der para escrever, sai com 2 (bloqueia,
// com ou sem failClosed) e o motivo vai para o stderr; um allow que não saiu
// também vira bloqueio, porque saída vazia com failClosed já bloqueia.
func writeCursor(stdout, stderr io.Writer, d cursorDecision, reason string) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		if reason == "" {
			reason = "iron: não consegui responder ao Cursor"
		}
		fmt.Fprintln(stderr, reason)
		return exitBlock
	}
	return exitOK
}
