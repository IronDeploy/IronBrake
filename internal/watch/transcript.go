package watch

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"
)

// Call é uma chamada de shell (ferramenta Bash) vista no transcript.
type Call struct {
	Session string
	ID      string
	Command string
	At      time.Time // quando o agente a emitiu
	DoneAt  time.Time // quando o resultado voltou; zero = ainda sem resultado
}

type record struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"sessionId"`
	Timestamp   time.Time       `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
}

type block struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	Input     struct {
		Command string `json:"command"`
	} `json:"input"`
}

// event é o que uma linha do transcript acrescenta: chamadas novas e resultados.
type event struct {
	calls   []Call
	results map[string]time.Time // id da chamada → hora do resultado
}

// parseLine lê uma linha. Linha que não é JSON, ou de um tipo que não importa,
// devolve ok=false. Chamadas de subagentes (isSidechain) ficam de fora: não dá
// para saber se o hook as registra com a mesma sessão.
func parseLine(line []byte) (event, bool) {
	var r record
	if json.Unmarshal(line, &r) != nil || r.IsSidechain {
		return event{}, false
	}

	var msg struct {
		Content []block `json:"content"`
	}
	// "message" pode ser um texto simples em alguns tipos de linha.
	if json.Unmarshal(r.Message, &msg) != nil {
		return event{}, false
	}

	ev := event{results: map[string]time.Time{}}
	for _, b := range msg.Content {
		switch {
		case r.Type == "assistant" && b.Type == "tool_use" && b.Name == "Bash":
			ev.calls = append(ev.calls, Call{Session: r.SessionID, ID: b.ID, Command: b.Input.Command, At: r.Timestamp})
		case r.Type == "user" && b.Type == "tool_result" && b.ToolUseID != "":
			ev.results[b.ToolUseID] = r.Timestamp
		}
	}
	return ev, len(ev.calls) > 0 || len(ev.results) > 0
}

// ParseTranscript lê um transcript inteiro e devolve as chamadas de shell, com a
// hora do resultado quando ele já voltou.
func ParseTranscript(r io.Reader) ([]Call, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	var calls []Call
	results := map[string]time.Time{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		ev, ok := parseLine(line)
		if !ok {
			continue
		}
		calls = append(calls, ev.calls...)
		for id, at := range ev.results {
			results[id] = at
		}
	}
	for i := range calls {
		calls[i].DoneAt = results[calls[i].ID]
	}
	return calls, nil
}

const maxReadPerPoll = 8 << 20

// tail lê só o que foi acrescentado a um arquivo desde a última leitura.
type tail struct {
	path    string
	offset  int64
	partial []byte // fim de arquivo sem quebra de linha: a linha ainda está sendo escrita
}

// atEnd começa a leitura no fim atual do arquivo (só o que vier depois conta).
func (t *tail) atEnd() {
	if info, err := os.Stat(t.path); err == nil {
		t.offset = info.Size()
	}
}

// lines devolve as linhas completas acrescentadas desde a última chamada.
func (t *tail) lines() ([][]byte, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	if info.Size() < t.offset { // o arquivo foi recriado
		t.offset, t.partial = 0, nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}

	chunk, err := io.ReadAll(io.LimitReader(f, maxReadPerPoll))
	if err != nil {
		return nil, err
	}
	t.offset += int64(len(chunk))

	data := append(t.partial, chunk...)
	parts := bytes.Split(data, []byte("\n"))
	t.partial = bytes.Clone(parts[len(parts)-1])
	return parts[:len(parts)-1], nil
}
