package watch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
)

const (
	// DefaultGrace: depois que o resultado volta, espera um pouco antes de
	// dizer que faltou a decisão (o relógio do transcript e o do log diferem).
	DefaultGrace = 3 * time.Second

	// skew: a decisão é gravada entre a emissão da chamada e a volta do
	// resultado; esta folga cobre a diferença entre os relógios.
	skew = 2 * time.Second

	// forgetAfter: chamada sem resultado há mais que isso deixa de ser esperada.
	forgetAfter = time.Hour
)

// Gap é uma chamada de shell que rodou sem decisão do Iron Brake.
type Gap struct {
	Call  Call
	Class string
}

// Source diz como classificar um comando (a mesma classe que o hook grava) e
// onde estão as decisões registradas.
type Source struct {
	Classify func(command string) string
	Entries  func(since time.Time) ([]audit.Entry, error)

	// Format é o formato dos transcripts; nil = Claude Code.
	Format *Format

	// Keep, se não for nil, descarta as chamadas que não são deste projeto (os
	// transcripts do Antigravity de todos os projetos ficam numa pasta só).
	Keep func(Call) bool
}

func (s Source) format() Format {
	if s.Format != nil {
		return *s.Format
	}
	return ClaudeFormat
}

// Tracker guarda as chamadas ainda não conferidas e as decisões já usadas.
type Tracker struct {
	src     Source
	grace   time.Duration
	pending map[string]Call
	order   []string
	used    map[string]bool

	// Checked e Covered contam as chamadas conferidas e as que tinham decisão.
	Checked, Covered int
}

func NewTracker(src Source, grace time.Duration) *Tracker {
	return &Tracker{src: src, grace: grace, pending: map[string]Call{}, used: map[string]bool{}}
}

// Add registra chamadas e resultados vistos no transcript.
func (t *Tracker) Add(ev event) {
	for _, c := range ev.calls {
		if t.src.Keep != nil && !t.src.Keep(c) {
			continue
		}
		if _, seen := t.pending[c.ID]; !seen {
			t.order = append(t.order, c.ID)
		}
		t.pending[c.ID] = c
	}
	for id, at := range ev.results {
		if c, ok := t.pending[id]; ok {
			c.DoneAt = at
			t.pending[id] = c
		}
	}
}

// Check confere as chamadas cujo resultado voltou há pelo menos o prazo de
// graça e devolve as que não têm decisão. Cada chamada é conferida uma vez.
func (t *Tracker) Check(now time.Time) ([]Gap, error) {
	var due []Call
	for _, id := range t.order {
		c, ok := t.pending[id]
		if !ok {
			continue
		}
		switch {
		case !c.DoneAt.IsZero() && now.Sub(c.DoneAt) >= t.grace:
			due = append(due, c)
		case c.DoneAt.IsZero() && now.Sub(c.At) > forgetAfter:
			delete(t.pending, id)
		}
	}
	if len(due) == 0 {
		t.compact()
		return nil, nil
	}

	earliest := due[0].At
	for _, c := range due {
		if c.At.Before(earliest) {
			earliest = c.At
		}
	}
	entries, err := t.src.Entries(earliest.Add(-skew))
	if err != nil {
		return nil, err
	}

	var gaps []Gap
	for _, c := range due {
		class := t.src.Classify(c.Command)
		t.Checked++
		if t.take(c, class, entries) {
			t.Covered++
		} else {
			gaps = append(gaps, Gap{Call: c, Class: class})
		}
		delete(t.pending, c.ID)
	}
	t.compact()
	return gaps, nil
}

// take procura uma decisão do hook para a chamada e a marca como usada, para
// dois comandos iguais precisarem de duas decisões.
func (t *Tracker) take(c Call, class string, entries []audit.Entry) bool {
	for _, e := range entries {
		if e.Rule == "watch" || e.Decision == "gap" || e.Class != class {
			continue
		}
		// Sem session_id no evento o hook não sabe a sessão: aceita para qualquer uma.
		if e.Session != "" && e.Session != c.Session {
			continue
		}
		if e.Time.Before(c.At.Add(-skew)) || e.Time.After(c.DoneAt.Add(skew)) {
			continue
		}
		key := e.Time.Format(time.RFC3339Nano) + "|" + e.Session + "|" + e.Class
		if t.used[key] {
			continue
		}
		t.used[key] = true
		return true
	}
	return false
}

func (t *Tracker) compact() {
	kept := t.order[:0]
	for _, id := range t.order {
		if _, ok := t.pending[id]; ok {
			kept = append(kept, id)
		}
	}
	t.order = kept
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// ProjectDirName é o nome da pasta onde o Claude Code guarda os transcripts de
// um projeto: o caminho com tudo que não é letra ou número trocado por "-".
func ProjectDirName(projectPath string) string {
	return nonAlnum.ReplaceAllString(projectPath, "-")
}

// TranscriptDir devolve ~/.claude/projects/<projeto>.
func TranscriptDir(home, projectPath string) string {
	return filepath.Join(home, ".claude", "projects", ProjectDirName(projectPath))
}

// transcripts lista os .jsonl comuns de uma pasta (os transcripts de sessão).
func transcripts(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	return paths
}
