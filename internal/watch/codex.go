package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CodexFormat lê os rollouts do Codex CLI (verificado com o 0.159.3):
// ~/.codex/sessions/AAAA/MM/DD/rollout-<data>-<uuid>.jsonl, uma linha JSON por
// item, com timestamp ISO. O uuid do nome é o session_id do hook, e a pasta do
// projeto vem do session_meta/turn_context (cwd).
//
// Nos testes TODAS as chamadas de shell (50 em 40 rollouts) eram um item
// "custom_tool_call" de nome "exec" cujo input é um trecho de JavaScript que
// chama tools.exec_command({cmd: "..."}); o resultado é o "custom_tool_call_output"
// do mesmo call_id. Por isso o comando é lido de dentro do JavaScript: só
// entende cmd escrito como string literal ("...", '...' ou `...` sem ${}) seguido
// de vírgula ou chave: concatenação ("a" + "b") ou variável não é lida, para nunca
// ler um comando pela metade. Um comando escrito de outro jeito não é visto, e outro
// modelo pode usar outro caminho (não verificado).
var CodexFormat = Format{Files: codexFiles, NewParser: codexParser}

// codexSlack: no "codex exec" o Codex grava o resultado de uma chamada cujo hook
// demora (a janela espera até 45 s) ANTES de o hook terminar; verificado com
// transcripts reais (resultado 31 s depois da chamada, decisão aos 45 s).
const codexSlack = 60 * time.Second

// CodexSessionsDir é a pasta de sessões do Codex (CODEX_HOME, ou ~/.codex).
func CodexSessionsDir(home string) string {
	if custom := os.Getenv("CODEX_HOME"); custom != "" {
		return filepath.Join(custom, "sessions")
	}
	return filepath.Join(home, ".codex", "sessions")
}

func codexFiles(dir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dir, "*", "*", "*", "rollout-*.jsonl"))
	return paths
}

var codexSessionID = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

type codexLine struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		CallID string `json:"call_id"`
		Input  string `json:"input"`
		Cwd    string `json:"cwd"`
	} `json:"payload"`
}

func codexParser(path string) parser {
	session := filepath.Base(path)
	if m := codexSessionID.FindStringSubmatch(session); m != nil {
		session = m[1]
	}
	cwd := ""
	commands := map[string]int{} // call_id → quantos comandos o script tinha

	return func(line []byte) (event, bool) {
		var l codexLine
		if json.Unmarshal(line, &l) != nil {
			return event{}, false
		}
		ev := event{results: map[string]time.Time{}}

		switch {
		case l.Type == "session_meta" || l.Type == "turn_context":
			if l.Payload.Cwd != "" {
				cwd = l.Payload.Cwd
			}
		case l.Type == "response_item" && l.Payload.Type == "custom_tool_call" && l.Payload.Name == "exec":
			cmds := codexCommands(l.Payload.Input)
			commands[l.Payload.CallID] = len(cmds)
			for i, cmd := range cmds {
				ev.calls = append(ev.calls, Call{
					Session: session, ID: codexCallID(l.Payload.CallID, i), Command: cmd, Cwd: cwd, At: l.Timestamp, Slack: codexSlack,
				})
			}
		case l.Type == "response_item" && l.Payload.Type == "custom_tool_call_output":
			for i := 0; i < commands[l.Payload.CallID]; i++ {
				ev.results[codexCallID(l.Payload.CallID, i)] = l.Timestamp
			}
		}
		return ev, len(ev.calls) > 0 || len(ev.results) > 0
	}
}

func codexCallID(callID string, index int) string { return fmt.Sprintf("%s#%d", callID, index) }

// execCommandCall acha cada tools.exec_command({... cmd: <string literal> ...}).
var execCommandCall = regexp.MustCompile("tools\\.exec_command\\(\\s*\\{[^}]*?\\bcmd\\s*:\\s*(\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`(?:[^`\\\\$]|\\\\.)*`)\\s*[,}]")

// codexCommands devolve os comandos das chamadas tools.exec_command do script.
// Literal que não dá para decodificar sem risco de errar é ignorado.
func codexCommands(script string) []string {
	var cmds []string
	for _, m := range execCommandCall.FindAllStringSubmatch(script, -1) {
		if cmd, ok := jsString(m[1]); ok {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// jsString decodifica um literal de string do JavaScript (com aspas incluídas).
func jsString(literal string) (string, bool) {
	body := literal[1 : len(literal)-1]
	if literal[0] == '"' {
		var s string
		if json.Unmarshal([]byte(literal), &s) == nil {
			return s, true
		}
		return "", false
	}
	// '...' e `...`: só as escapes simples; qualquer outra deixa o literal de fora.
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			out.WriteByte(body[i])
			continue
		}
		i++
		if i >= len(body) {
			return "", false
		}
		switch body[i] {
		case '\\', '\'', '"', '`':
			out.WriteByte(body[i])
		case 'n':
			out.WriteByte('\n')
		case 't':
			out.WriteByte('\t')
		default:
			return "", false
		}
	}
	return out.String(), true
}
