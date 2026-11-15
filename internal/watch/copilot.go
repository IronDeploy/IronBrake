package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CopilotFormat lê as sessões do Copilot CLI (verificado com o 1.0.112):
// ~/.copilot/session-state/<sessão>/events.jsonl, uma linha JSON por evento, com
// timestamp ISO. O nome da pasta é o sessionId que o hook recebe. A chamada de
// shell é um "tool.execution_start" (toolName "bash" ou "powershell",
// arguments.command) e o resultado é o "tool.execution_complete" do mesmo
// toolCallId, também quando o hook bloqueou (success false, code "denied").
//
// O evento da chamada não traz a pasta: ela vem do workspace.yaml da sessão
// (cwd). O Copilot ainda grava "hook.start" e "hook.end" no mesmo arquivo, mas
// o watch não os usa: o que ele confere é o log do Iron Brake, de fora.
//
// Uma chamada que o Copilot recusou (tool.execution_complete com error.code
// "denied") não rodou, e fica de fora da conferência.
var CopilotFormat = Format{Files: copilotFiles, NewParser: copilotParser}

// CopilotSessionsDir é a pasta de sessões do Copilot (COPILOT_HOME, ou ~/.copilot).
func CopilotSessionsDir(home string) string {
	if custom := os.Getenv("COPILOT_HOME"); custom != "" {
		return filepath.Join(custom, "session-state")
	}
	return filepath.Join(home, ".copilot", "session-state")
}

func copilotFiles(dir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dir, "*", "events.jsonl"))
	return paths
}

type copilotLine struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      struct {
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Arguments  struct {
			Command string `json:"command"`
		} `json:"arguments"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	} `json:"data"`
}

func copilotParser(path string) parser {
	dir := filepath.Dir(path)
	session := filepath.Base(dir)
	cwd := ""

	return func(line []byte) (event, bool) {
		var l copilotLine
		if json.Unmarshal(line, &l) != nil {
			return event{}, false
		}
		ev := event{results: map[string]time.Time{}}

		switch l.Type {
		case "tool.execution_start":
			if (l.Data.ToolName == "bash" || l.Data.ToolName == "powershell") && l.Data.Arguments.Command != "" {
				if cwd == "" {
					cwd = copilotWorkspaceCwd(dir)
				}
				ev.calls = append(ev.calls, Call{
					Session: session, ID: l.Data.ToolCallID, Command: l.Data.Arguments.Command, Cwd: cwd, At: l.Timestamp,
				})
			}
		case "tool.execution_complete":
			if l.Data.ToolCallID != "" {
				ev.results[l.Data.ToolCallID] = l.Timestamp
				// "denied": um hook (o do Iron Brake ou outro) recusou a chamada.
				// O Copilot roda os hooks em sequência e para no primeiro deny, então
				// o Iron Brake pode nem ter sido consultado, e o comando não rodou.
				if l.Data.Error.Code == "denied" {
					ev.blocked = append(ev.blocked, l.Data.ToolCallID)
				}
			}
		}
		return ev, len(ev.calls) > 0 || len(ev.results) > 0
	}
}

// copilotWorkspaceCwd lê o "cwd:" do workspace.yaml da sessão. Vazio se não der
// (a chamada fica sem pasta e o watch a ignora, em vez de acusar outro projeto).
func copilotWorkspaceCwd(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "workspace.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, ok := strings.CutPrefix(line, "cwd:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		} else if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			var s string
			if json.Unmarshal([]byte(value), &s) != nil {
				return ""
			}
			value = s
		}
		return value
	}
	return ""
}
