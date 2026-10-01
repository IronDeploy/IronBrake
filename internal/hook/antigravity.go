package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// Antigravity é o Antigravity CLI (Google, comando "agy"). Contrato verificado
// com o agente real (1.2.14), em doc/agents.md: o hook roda antes da permissão
// do agente; deny (JSON) e qualquer saída de erro bloqueiam; um hook que
// estoura o prazo é morto e o comando é bloqueado; "allow" e saída vazia
// deixam a decisão para a permissão do agente.
var Antigravity Agent = antigravityAgent{}

type antigravityAgent struct{}

func (antigravityAgent) Name() string { return "antigravity" }

// CanAsk é falso de propósito. Com --dangerously-skip-permissions o "ask" do
// hook não segura nada (o comando executa), então o Iron Brake pergunta na
// própria janela e, sem janela, bloqueia.
//
// AppID: o Antigravity não tem onde gravar variáveis de ambiente (o settings.json
// é global e não tem chave env), então o "iron init" só informa o valor; quem
// o exporta no shell antes de abrir o agy é a pessoa. O agy repassa o ambiente
// aos comandos (verificado).
func (antigravityAgent) Capabilities() Capabilities {
	return Capabilities{CanAsk: false, Timeout: AntigravityTimeout, AppID: "iron-antigravity"}
}

// AntigravityTimeout é o prazo gravado no hooks.json. Sem o campo o padrão é
// 30 s (medido); estourado, o agente mata o hook e bloqueia o comando.
const AntigravityTimeout = 600 * time.Second

// AntigravityShellTool é a ferramenta de shell, e o matcher do hooks.json.
const AntigravityShellTool = "run_command"

// antigravityEvent é o stdin do PreToolUse (só os campos usados). O comando
// vem em toolCall.args.CommandLine.
type antigravityEvent struct {
	ToolCall struct {
		Name string `json:"name"`
		Args struct {
			CommandLine string `json:"CommandLine"`
			Cwd         string `json:"Cwd"`
		} `json:"args"`
	} `json:"toolCall"`
	ConversationID string   `json:"conversationId"`
	WorkspacePaths []string `json:"workspacePaths"`
}

func (antigravityAgent) ParseEvent(stdin io.Reader) (Event, error) {
	var in antigravityEvent
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return Event{}, err
	}
	return Event{
		Command:   in.ToolCall.Args.CommandLine,
		Tool:      in.ToolCall.Name,
		Shell:     in.ToolCall.Name == AntigravityShellTool,
		SessionID: in.ConversationID,
		Cwd:       antigravityCwd(in.ToolCall.Args.Cwd, in.WorkspacePaths),
	}, nil
}

// antigravityCwd escolhe a pasta de referência. O hook roda em .agents/, não
// no projeto, e o Antigravity não exporta variável com a raiz. Dentro de um
// workspace, vale a raiz dele: a política do projeto é achada e um caminho
// relativo avaliado a partir da raiz só erra para o lado de bloquear a mais.
// Fora de todo workspace, o comando roda onde o Cwd diz, e é ele que vale.
func antigravityCwd(cwd string, workspaces []string) string {
	if cwd == "" {
		if len(workspaces) > 0 {
			return workspaces[0]
		}
		return ""
	}
	clean := filepath.Clean(cwd)
	for _, ws := range workspaces {
		ws = filepath.Clean(ws)
		if rel, err := filepath.Rel(ws, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ws
		}
	}
	return clean
}

type antigravityDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func (antigravityAgent) Respond(stdout, stderr io.Writer, decision Decision, reason string) int {
	switch decision {
	case Allow, UserApproved:
		// Sem saída: o agente decide pela permissão dele. Um "allow" em JSON
		// também não libera; e "{}" bloquearia.
		return exitOK
	case Deny, Ask:
		// O Antigravity ignora o ask com a aprovação automática ligada: bloqueia.
		return writeAntigravityDeny(stdout, stderr, reason)
	}
	return writeAntigravityDeny(stdout, stderr, fmt.Sprintf("iron: decisão desconhecida %q", decision))
}

// writeAntigravityDeny responde o deny em JSON (o motivo chega ao usuário e ao
// modelo). Se não der para escrever, sai com 2: qualquer saída de erro
// bloqueia no Antigravity.
func writeAntigravityDeny(stdout, stderr io.Writer, reason string) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(antigravityDecision{Decision: "deny", Reason: reason}); err != nil {
		fmt.Fprintln(stderr, reason)
		return exitBlock
	}
	return exitOK
}
