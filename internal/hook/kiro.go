package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"
)

// Kiro é o Kiro CLI (AWS), engines v2 e v3. Contrato verificado com o agente
// real (2.26.1), em doc/agents.md: só a saída 2 bloqueia, o stderr vai ao
// modelo, e JSON de resposta é ignorado (inclusive deny e ask).
var Kiro Agent = kiroAgent{}

type kiroAgent struct{}

func (kiroAgent) Name() string { return "kiro" }

// Timeout é o que o "iron init" grava: timeout_ms no agente v2 e timeout (s)
// no .kiro/hooks do v3. Sem o campo, o v2 libera o comando após ~10 s.
func (kiroAgent) Capabilities() Capabilities {
	return Capabilities{CanAsk: false, Timeout: KiroTimeout}
}

// KiroTimeout é o prazo gravado no hook do Kiro.
const KiroTimeout = 600 * time.Second

// kiroEvent é o stdin do preToolUse. O v2 envia "preToolUse"/"shell" e o v3
// "PreToolUse"/"execute_bash"; o resto é igual (só os campos usados).
type kiroEvent struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

// kiroShellTools são os nomes da ferramenta de shell: "shell" (v2),
// "execute_bash" (v3) e "execute_cmd" (Windows, só documentado).
var kiroShellTools = []string{"shell", "execute_bash", "execute_cmd"}

// KiroShellMatcherV3 é o matcher (regex) do .kiro/hooks do v3. O "*" do v2 não
// compila como regex e o v3 descarta o hook sem avisar.
const KiroShellMatcherV3 = "^(execute_bash|shell|execute_cmd)$"

func (kiroAgent) ParseEvent(stdin io.Reader) (Event, error) {
	var in kiroEvent
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return Event{}, err
	}
	// tool_input.cwd do v3 é ignorado de propósito: quem o preenche é o modelo,
	// e apontá-lo para outra pasta tiraria a política do projeto.
	return Event{
		Command:   in.ToolInput.Command,
		Tool:      in.ToolName,
		Shell:     slices.Contains(kiroShellTools, in.ToolName),
		SessionID: in.SessionID,
		Cwd:       in.Cwd,
	}, nil
}

func (kiroAgent) Respond(stdout, stderr io.Writer, decision Decision, reason string) int {
	switch decision {
	case Allow, UserApproved:
		return exitOK
	case Deny:
		fmt.Fprintln(stderr, reason)
		return exitBlock
	case Ask:
		// O Kiro ignora o JSON de ask: sem janela, bloqueia.
		fmt.Fprintln(stderr, reason)
		return exitBlock
	}

	fmt.Fprintf(stderr, "iron: decisão desconhecida %q\n", decision)
	return exitBlock
}
