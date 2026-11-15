package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Copilot é o GitHub Copilot CLI. Contrato da documentação oficial
// (docs.github.com/copilot/reference/hooks-reference) e payload capturado com o
// agente real (1.0.112, interativo): o evento "preToolUse" chega em camelCase,
// com a ferramenta de shell "bash" (ou "powershell") e toolArgs como objeto.
// Na saída, o JSON permissionDecision "deny" bloqueia; saída vazia deixa a
// decisão para a permissão do agente. No preToolUse qualquer saída ≠ 0 também
// bloqueia, mas estourar o prazo LIBERA o comando.
var Copilot Agent = copilotAgent{}

type copilotAgent struct{}

func (copilotAgent) Name() string { return "copilot" }

// Ferramentas de shell do Copilot, e o evento gravado no arquivo de hooks.
const (
	CopilotShellTool      = "bash"
	CopilotPowerShellTool = "powershell"
	CopilotEvent          = "preToolUse"
)

// CopilotTimeout é o prazo gravado em timeoutSec. O padrão do agente é 30 s e,
// estourado, o comando é liberado: a janela de confirmação não cabe nele.
const CopilotTimeout = 600 * time.Second

// CanAsk é falso até o "ask" ser verificado com o agente real: o Copilot o trata
// como deny em autopilot, e o Iron Brake pergunta na própria janela.
func (copilotAgent) Capabilities() Capabilities {
	return Capabilities{CanAsk: false, Timeout: CopilotTimeout, AppID: "iron-copilot"}
}

// copilotEvent é o stdin do preToolUse (só os campos usados). toolArgs é
// "unknown" na documentação; capturado, é um objeto, e a variante PascalCase
// pode entregá-lo como texto JSON.
type copilotEvent struct {
	ToolName  string          `json:"toolName"`
	ToolArgs  json.RawMessage `json:"toolArgs"`
	SessionID string          `json:"sessionId"`
	Cwd       string          `json:"cwd"`
}

func (copilotAgent) ParseEvent(stdin io.Reader) (Event, error) {
	var in copilotEvent
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return Event{}, err
	}
	command, err := copilotCommand(in.ToolArgs)
	if err != nil {
		return Event{}, fmt.Errorf("toolArgs: %w", err)
	}
	shell := in.ToolName == CopilotShellTool || in.ToolName == CopilotPowerShellTool
	if !shell {
		command = ""
	}
	return Event{
		Command:    command,
		Tool:       in.ToolName,
		Shell:      shell,
		PowerShell: in.ToolName == CopilotPowerShellTool,
		SessionID:  in.SessionID,
		Cwd:        in.Cwd,
	}, nil
}

// copilotCommand extrai o comando de toolArgs, objeto ou texto JSON. Sem
// toolArgs (ferramentas sem argumentos) devolve vazio.
func copilotCommand(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		raw = json.RawMessage(text)
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	return args.Command, nil
}

type copilotDecision struct {
	Decision string `json:"permissionDecision"`
	Reason   string `json:"permissionDecisionReason"`
}

func (copilotAgent) Respond(stdout, stderr io.Writer, decision Decision, reason string) int {
	switch decision {
	case Allow, UserApproved:
		// Sem saída: o agente segue a permissão dele. Um "allow" em JSON
		// poderia pular a pergunta que o próprio agente faria.
		return exitOK
	case Deny, Ask:
		return writeCopilotDeny(stdout, stderr, reason)
	}
	return writeCopilotDeny(stdout, stderr, fmt.Sprintf("iron: decisão desconhecida %q", decision))
}

// writeCopilotDeny responde o deny em JSON (o motivo chega ao modelo). Se não
// der para escrever, sai com 2: no preToolUse todo código ≠ 0 bloqueia.
func writeCopilotDeny(stdout, stderr io.Writer, reason string) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(copilotDecision{Decision: "deny", Reason: reason}); err != nil {
		fmt.Fprintln(stderr, reason)
		return exitBlock
	}
	return exitOK
}
