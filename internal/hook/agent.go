package hook

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Agent traduz o protocolo de hook de um agente de código: lê o evento que ele
// envia e escreve a resposta que ele entende.
type Agent interface {
	Name() string

	Capabilities() Capabilities

	// ParseEvent lê o evento do stdin.
	ParseEvent(stdin io.Reader) (Event, error)

	// Respond escreve a resposta e devolve o código de saída. Qualquer falha
	// ou decisão desconhecida bloqueia.
	Respond(stdout, stderr io.Writer, decision Decision, reason string) int
}

// Capabilities descreve o que o agente entende e quanto tempo dá ao hook.
type Capabilities struct {
	// CanAsk: o agente entende a decisão "ask" e pergunta ao usuário. Sem isso,
	// um ask que a janela não resolveu vira deny (nunca allow).
	CanAsk bool

	// Timeout é o prazo que o agente dá ao hook: o valor que o "iron init"
	// grava na configuração dele, ou o fixo do agente. Estourado, quase todos
	// os agentes liberam o comando.
	Timeout time.Duration

	// AppID é a etiqueta que o "iron init" põe em AWS_SDK_UA_APP_ID no ambiente do
	// agente: o SDK da AWS a acrescenta ao user agent (app/<AppID>), e o
	// CloudTrail a registra em cada chamada de API. Vazio: o agente não tem como
	// receber a variável.
	AppID string
}

// Budget é como o hook reparte o Timeout do agente.
type Budget struct {
	// Deadline é o prazo total do hook; estourado, ele responde deny antes de
	// o agente desistir.
	Deadline time.Duration

	// DialogWait é quanto a janela espera pelo clique. Zero: não sobrou tempo
	// para a janela, e o ask segue como se ela não estivesse disponível.
	DialogWait time.Duration
}

const (
	// deadlineMarginDiv: o Deadline fica 1/15 abaixo do Timeout (600 s → 560 s).
	deadlineMarginDiv = 15

	// dialogReserve sai do Deadline antes da janela: o terraform show (30 s) e
	// uma folga.
	dialogReserve = 80 * time.Second

	// minDialogWait: esperar menos que isso não dá tempo de ler o motivo.
	minDialogWait = 10 * time.Second
)

func (c Capabilities) Budget() Budget {
	deadline := c.Timeout - c.Timeout/deadlineMarginDiv
	wait := deadline - dialogReserve
	if wait < minDialogWait {
		wait = 0
	}
	return Budget{Deadline: deadline, DialogWait: wait}
}

// DefaultAgent é o agente de "iron hook" sem --agent.
const DefaultAgent = "claude"

var agents = []Agent{Claude, Kiro}

// Lookup acha o agente pelo nome, sem diferenciar maiúsculas.
func Lookup(name string) (Agent, error) {
	for _, a := range agents {
		if strings.EqualFold(a.Name(), name) {
			return a, nil
		}
	}
	return nil, fmt.Errorf("agente desconhecido %q (suportados: %s)", name, strings.Join(AgentNames(), ", "))
}

func AgentNames() []string {
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name())
	}
	slices.Sort(names)
	return names
}

// projectDirVars são as variáveis em que os agentes informam a raiz do
// projeto. Gemini CLI e Cursor também exportam CLAUDE_PROJECT_DIR como alias.
var projectDirVars = []string{"CLAUDE_PROJECT_DIR"}

// ProjectDir devolve a raiz do projeto informada pelo agente, ou "" se nenhuma
// variável conhecida estiver definida.
func ProjectDir(getenv func(string) string) string {
	for _, name := range projectDirVars {
		if dir := getenv(name); dir != "" {
			return dir
		}
	}
	return ""
}
