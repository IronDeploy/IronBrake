package hook

// PreToolUseEvent é o JSON que o Claude Code envia pelo stdin antes de
// executar uma ferramenta (só os campos usados).
type PreToolUseEvent struct {
	ToolName  string    `json:"tool_name"`
	ToolInput ToolInput `json:"tool_input"`

	SessionID string `json:"session_id"`

	// Cwd é onde o comando vai rodar; o shell do Claude Code lembra os cd.
	Cwd string `json:"cwd"`
}

type ToolInput struct {
	Command string `json:"command"`
}
