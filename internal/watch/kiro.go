package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// KiroFormat lê os dois formatos de sessão do Kiro CLI (verificado com o 2.26.1):
//
//   - v3: ~/.kiro/sessions/<projeto>/sess_<uuid>/messages.jsonl, uma linha JSON por
//     evento, com timestamp ISO. A chamada de shell é payload.type "tool_call"
//     (toolName execute_bash, args.command) e o resultado é o "tool_result" do
//     mesmo toolCallId. O args.cwd quase sempre vem vazio: vale o workspacePaths
//     do session.json ao lado. O nome da pasta da sessão é o session_id do hook.
//     Chamadas com status "denied" não executaram e ficam de fora.
//   - v2: ~/.kiro/sessions/cli/<uuid>.jsonl ({"kind","data"} por mensagem). A
//     chamada é um toolUse (name "shell", input.command e working_dir) dentro de
//     um AssistantMessage, e o resultado é um toolResult do mesmo toolUseId. Só
//     algumas linhas têm horário (data.meta.timestamp, em segundos), em geral só
//     o prompt: a chamada vale a partir do último horário visto e, como não há
//     horário de término, a decisão do hook pode vir até kiroV2Slack depois.
var KiroFormat = Format{Files: kiroFiles, NewParser: kiroParser}

// KiroSessionsDir é a pasta de sessões do Kiro CLI.
func KiroSessionsDir(home string) string { return filepath.Join(home, ".kiro", "sessions") }

func kiroFiles(dir string) []string {
	v2, _ := filepath.Glob(filepath.Join(dir, "cli", "*.jsonl"))
	v3, _ := filepath.Glob(filepath.Join(dir, "*", "sess_*", "messages.jsonl"))
	return append(v2, v3...)
}

func kiroParser(path string) parser {
	if filepath.Base(path) == "messages.jsonl" {
		return kiroV3Parser(path)
	}
	return kiroV2Parser(path)
}

// kiroShellTools são os nomes da ferramenta de shell nas duas versões.
var kiroShellTools = []string{"shell", "execute_bash", "execute_cmd"}

// ---- v3

type kiroV3Line struct {
	Timestamp time.Time `json:"timestamp"`
	Payload   struct {
		Type       string `json:"type"`
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Status     string `json:"status"`
		Args       struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
		} `json:"args"`
	} `json:"payload"`
}

func kiroV3Parser(path string) parser {
	dir := filepath.Dir(path)
	session := filepath.Base(dir)
	workspace := kiroWorkspace(filepath.Join(dir, "session.json"))

	return func(line []byte) (event, bool) {
		var l kiroV3Line
		if json.Unmarshal(line, &l) != nil {
			return event{}, false
		}
		ev := event{results: map[string]time.Time{}}
		switch l.Payload.Type {
		case "tool_call":
			if slices.Contains(kiroShellTools, l.Payload.ToolName) && l.Payload.Status != "denied" {
				cwd := l.Payload.Args.Cwd
				if cwd == "" {
					cwd = workspace
				}
				ev.calls = append(ev.calls, Call{
					Session: session, ID: l.Payload.ToolCallID, Command: l.Payload.Args.Command, Cwd: cwd, At: l.Timestamp,
				})
			}
		case "tool_result":
			ev.results[l.Payload.ToolCallID] = l.Timestamp
		}
		return ev, len(ev.calls) > 0 || len(ev.results) > 0
	}
}

// kiroWorkspace lê a pasta do projeto do session.json; sem ele devolve "".
func kiroWorkspace(sessionJSON string) string {
	data, err := os.ReadFile(sessionJSON)
	if err != nil {
		return ""
	}
	var s struct {
		WorkspacePaths []string `json:"workspacePaths"`
	}
	if json.Unmarshal(data, &s) != nil || len(s.WorkspacePaths) == 0 {
		return ""
	}
	return s.WorkspacePaths[0]
}

// ---- v2

type kiroV2Line struct {
	Kind string `json:"kind"`
	Data struct {
		Meta struct {
			Timestamp int64 `json:"timestamp"`
		} `json:"meta"`
		Content []struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		} `json:"content"`
	} `json:"data"`
}

// kiroV2Slack é o prazo máximo de um hook do Kiro (o que o iron init grava): a
// decisão da chamada vem dentro dele.
const kiroV2Slack = 10 * time.Minute

func kiroV2Parser(path string) parser {
	base := strings.TrimSuffix(path, ".jsonl")
	session := filepath.Base(base)
	meta := kiroV2Meta(base + ".json")
	last := meta.CreatedAt // até aparecer um horário na própria sessão
	started := map[string]time.Time{}

	return func(line []byte) (event, bool) {
		var l kiroV2Line
		if json.Unmarshal(line, &l) != nil {
			return event{}, false
		}
		ev := event{results: map[string]time.Time{}}

		if ts := l.Data.Meta.Timestamp; ts > 0 {
			last = time.Unix(ts, 0)
		}

		for _, c := range l.Data.Content {
			switch c.Kind {
			case "toolUse":
				var use struct {
					ToolUseID string `json:"toolUseId"`
					Name      string `json:"name"`
					Input     struct {
						Command    string `json:"command"`
						WorkingDir string `json:"working_dir"`
					} `json:"input"`
				}
				if json.Unmarshal(c.Data, &use) == nil && slices.Contains(kiroShellTools, use.Name) && !last.IsZero() {
					cwd := use.Input.WorkingDir
					if cwd == "" {
						cwd = meta.Cwd // working_dir costuma vir vazio: vale a pasta da sessão
					}
					started[use.ToolUseID] = last
					ev.calls = append(ev.calls, Call{
						Session: session, ID: use.ToolUseID, Command: use.Input.Command, Cwd: cwd, At: last, Slack: kiroV2Slack,
					})
				}
			case "toolResult":
				var res struct {
					ToolUseID string `json:"toolUseId"`
				}
				// Sem horário de término: o resultado conta no horário da chamada, e a
				// decisão pode vir até kiroV2Slack depois.
				if json.Unmarshal(c.Data, &res) == nil && !started[res.ToolUseID].IsZero() {
					ev.results[res.ToolUseID] = started[res.ToolUseID]
				}
			}
		}
		return ev, len(ev.calls) > 0 || len(ev.results) > 0
	}
}

// kiroMeta é o que o .json da sessão v2 diz.
type kiroMeta struct {
	Cwd       string    `json:"cwd"`
	CreatedAt time.Time `json:"created_at"`
}

// kiroV2Meta lê o .json da sessão v2; sem ele devolve o zero (o parser então
// só confere chamadas depois de ver um horário na própria sessão).
func kiroV2Meta(metaJSON string) kiroMeta {
	var m kiroMeta
	if data, err := os.ReadFile(metaJSON); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}
