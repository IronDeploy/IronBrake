package awsgate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/IronDeploy/IronBrake/internal/filelock"
	"github.com/IronDeploy/IronBrake/internal/safefile"
)

const (
	lockTimeout  = 2 * time.Second
	maxStateSize = 1 << 20
	maxGrants    = 200
)

// Approvals guarda por quanto tempo o usuário liberou um agente em um perfil, para
// não perguntar a cada processo (o SDK roda o portão uma vez por processo).
// Só guarda um hash do par agente/perfil e a hora em que a liberação acaba.
type Approvals struct {
	Dir string
}

// DefaultApprovalsDir é <cache do usuário>/ironbrake/awsgate, ou "" se não houver.
func DefaultApprovalsDir() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "ironbrake", "awsgate")
}

type grants struct {
	Until map[string]time.Time `json:"until"`
}

func key(agent, label string) string {
	sum := sha256.Sum256([]byte(agent + "\x00" + label))
	return hex.EncodeToString(sum[:])
}

func (w Approvals) file() string { return filepath.Join(w.Dir, "grants.json") }

func (w Approvals) lock() (func(), error) {
	if w.Dir == "" {
		return nil, errors.New("sem pasta para a janela de aprovação")
	}
	if err := os.MkdirAll(w.Dir, 0o700); err != nil {
		return nil, err
	}
	return filelock.Acquire(filepath.Join(w.Dir, "grants.lock"), lockTimeout)
}

func (w Approvals) read() (grants, error) {
	g := grants{Until: map[string]time.Time{}}
	data, err := safefile.Read(w.file(), maxStateSize)
	if errors.Is(err, fs.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		return g, err
	}
	if err := json.Unmarshal(data, &g); err != nil {
		return grants{Until: map[string]time.Time{}}, err
	}
	if g.Until == nil {
		g.Until = map[string]time.Time{}
	}
	return g, nil
}

// PromptLock serializa as perguntas: o SDK roda o portão uma vez por processo, e
// o agente pode rodar vários comandos ao mesmo tempo. Quem pega a trava pergunta;
// os outros esperam e, depois, conferem de novo se já há liberação.
func (a Approvals) PromptLock(timeout time.Duration) (release func(), err error) {
	if a.Dir == "" {
		return nil, errors.New("sem pasta para a janela de aprovação")
	}
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return nil, err
	}
	return filelock.Acquire(filepath.Join(a.Dir, "prompt.lock"), timeout)
}

// Valid diz se há uma liberação ainda vigente. Qualquer erro de leitura vale
// como "não": na dúvida, pergunta.
func (w Approvals) Valid(agent, label string, now time.Time) bool {
	release, err := w.lock()
	if err != nil {
		return false
	}
	defer release()

	g, err := w.read()
	if err != nil {
		return false
	}
	return now.Before(g.Until[key(agent, label)])
}

// Grant libera o agente neste perfil até until e apaga as liberações vencidas.
func (w Approvals) Grant(agent, label string, now, until time.Time) error {
	release, err := w.lock()
	if err != nil {
		return err
	}
	defer release()

	g, err := w.read()
	if err != nil {
		g = grants{Until: map[string]time.Time{}} // arquivo corrompido: recomeça
	}
	for k, t := range g.Until {
		if !now.Before(t) {
			delete(g.Until, k)
		}
	}
	if len(g.Until) >= maxGrants {
		g = grants{Until: map[string]time.Time{}}
	}
	g.Until[key(agent, label)] = until

	data, err := json.Marshal(g)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(w.Dir, "grants.tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), w.file())
}
