package watch

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// AntigravityFormat: ~/.gemini/antigravity-cli/brain/<conversa>/.system_generated/logs/transcript_full.jsonl,
// uma linha JSON por passo (verificado com o agy 1.2.14). A chamada de shell é
// um passo PLANNER_RESPONSE com tool_calls[{name:"run_command",args:{CommandLine,Cwd}}],
// e o resultado é o passo seguinte (GENERIC, status DONE ou ERROR, também
// quando o hook bloqueou). O horário tem precisão de 1 s, em UTC. A conversa
// é o nome da pasta, igual ao conversationId que o hook recebe.
//
// O created_at do passo de resultado é o INÍCIO dele: o hook pode gravar a
// decisão depois (a janela de confirmação espera o clique). Por isso a chamada
// só conta como terminada quando o modelo responde de novo, no PLANNER_RESPONSE
// dois passos depois, e é o horário dele que fecha o intervalo da decisão.
//
// Limite: num passo com mais de uma chamada só a primeira é conferida (a
// relação entre as chamadas e os passos seguintes não foi verificada).
var AntigravityFormat = Format{Files: agyFiles, NewParser: agyParser}

// BrainDir é a pasta das conversas do Antigravity CLI.
func BrainDir(home string) string { return filepath.Join(home, ".gemini", "antigravity-cli", "brain") }

func agyFiles(dir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dir, "*", ".system_generated", "logs", "transcript_full.jsonl"))
	return paths
}

type agyStep struct {
	StepIndex int       `json:"step_index"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	ToolCalls []struct {
		Name string `json:"name"`
		Args struct {
			CommandLine string `json:"CommandLine"`
			Cwd         string `json:"Cwd"`
		} `json:"args"`
	} `json:"tool_calls"`
}

func agyCallID(session string, step, index int) string {
	return fmt.Sprintf("%s#%d#%d", session, step, index)
}

func agyParser(path string) parser {
	// .../brain/<conversa>/.system_generated/logs/transcript_full.jsonl
	session := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))

	return func(line []byte) (event, bool) {
		var st agyStep
		if json.Unmarshal(line, &st) != nil {
			return event{}, false
		}

		ev := event{results: map[string]time.Time{}}
		switch st.Type {
		case "PLANNER_RESPONSE":
			for i, tc := range st.ToolCalls {
				if tc.Name == "run_command" {
					ev.calls = append(ev.calls, Call{
						Session: session, ID: agyCallID(session, st.StepIndex, i),
						Command: tc.Args.CommandLine, Cwd: tc.Args.Cwd, At: st.CreatedAt,
					})
				}
			}
		}
		// A resposta seguinte do modelo (passo n+2) termina a chamada do passo n.
		if st.Type == "PLANNER_RESPONSE" && st.StepIndex >= 2 {
			ev.results[agyCallID(session, st.StepIndex-2, 0)] = st.CreatedAt
		}
		return ev, len(ev.calls) > 0 || len(ev.results) > 0
	}
}

// InProject devolve o filtro das chamadas cujo Cwd está dentro de projectDir.
// Sem Cwd não dá para saber de que projeto é a chamada: fica de fora, para não
// acusar comandos de projetos que nunca tiveram o hook.
func InProject(projectDir string) func(Call) bool {
	root := filepath.Clean(projectDir)
	return func(c Call) bool {
		if c.Cwd == "" {
			return false
		}
		rel, err := filepath.Rel(root, filepath.Clean(c.Cwd))
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
}
