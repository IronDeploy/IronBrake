package watch

import (
	"os"
	"time"
)

// Watcher acompanha os transcripts de uma pasta e confere cada chamada.
type Watcher struct {
	dirs    []string
	tracker *Tracker
	tails   map[string]*tail
	started time.Time
}

// New acompanha o que for escrito a partir de agora: as linhas que já estavam
// nos arquivos não contam, e arquivos criados depois são lidos desde o começo.
func New(dirs []string, src Source, grace time.Duration, now time.Time) *Watcher {
	w := &Watcher{dirs: dirs, tracker: NewTracker(src, grace), tails: map[string]*tail{}, started: now}
	for _, dir := range dirs {
		for _, path := range transcripts(dir) {
			t := &tail{path: path}
			t.atEnd()
			w.tails[path] = t
		}
	}
	return w
}

// Poll lê o que os transcripts ganharam e devolve as lacunas já confirmadas.
func (w *Watcher) Poll(now time.Time) ([]Gap, error) {
	for _, dir := range w.dirs {
		for _, path := range transcripts(dir) {
			t, known := w.tails[path]
			if !known {
				t = &tail{path: path}
				w.tails[path] = t
			}
			lines, err := t.lines()
			if err != nil {
				if os.IsNotExist(err) {
					delete(w.tails, path)
					continue
				}
				return nil, err
			}
			for _, line := range lines {
				if ev, ok := parseLine(line); ok {
					w.tracker.Add(ev)
				}
			}
		}
	}
	return w.tracker.Check(now)
}

// Stats devolve quantas chamadas foram conferidas e quantas tinham decisão.
func (w *Watcher) Stats() (checked, covered int) {
	return w.tracker.Checked, w.tracker.Covered
}

// Scan confere de uma vez os transcripts de dirs: só as chamadas emitidas a
// partir de since, com resultado. Serve para o relatório do "iron watch --once".
func Scan(dirs []string, src Source, since time.Time) (gaps []Gap, checked, covered int, err error) {
	t := NewTracker(src, 0)
	for _, dir := range dirs {
		for _, path := range transcripts(dir) {
			if info, statErr := os.Stat(path); statErr != nil || info.ModTime().Before(since) {
				continue
			}
			f, openErr := os.Open(path)
			if openErr != nil {
				return nil, 0, 0, openErr
			}
			calls, parseErr := ParseTranscript(f)
			f.Close()
			if parseErr != nil {
				return nil, 0, 0, parseErr
			}
			for _, c := range calls {
				if c.At.Before(since) {
					continue
				}
				t.Add(event{calls: []Call{c}, results: map[string]time.Time{c.ID: c.DoneAt}})
			}
		}
	}
	// Tudo o que tem resultado já está vencido: confere com o relógio no fim dos tempos.
	gaps, err = t.Check(time.Now().Add(forgetAfter * 24))
	return gaps, t.Checked, t.Covered, err
}
