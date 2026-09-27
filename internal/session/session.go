package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/filelock"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/safefile"
)

const (
	repeatWindow = 5 * time.Minute
	askRepeats   = 3
	denyRepeats  = 6
	maxApplies   = 3
	maxResources = 20
	maxAge       = 24 * time.Hour // sessão parada há mais tempo é apagada
	lockTimeout  = 2 * time.Second
	maxStateSize = 1 << 20
)

const (
	repeatAskReason  = "Iron Brake: o mesmo comando de infraestrutura rodou %d vezes em 5 minutos nesta sessão. o agente pode estar em loop; confirme antes de repetir."
	repeatDenyReason = "Iron Brake: bloqueado: o mesmo comando de infraestrutura rodou %d vezes em 5 minutos nesta sessão. o agente parece estar em loop; peça ao usuário para revisar o que está acontecendo."
	limitReason      = "Iron Brake: esta sessão já fez %d applies e alterou %d recursos (limites: 3 applies e 20 recursos). confirme antes de continuar."
	stateErrorReason = "Iron Brake: não consegui ler ou gravar o estado desta sessão, então não sei se o agente está repetindo comandos. confirme antes de executar."
)

// Activity é o que um comando representa para a sessão.
type Activity struct {
	Command   string // normalizado, só se mexe em infra
	Applies   int
	Resources int
}

func (a Activity) relevant() bool {
	return a.Command != "" || a.Applies > 0
}

// State é o que fica gravado por sessão: nunca o comando, só um hash dele.
type State struct {
	Salt      string  `json:"salt"`
	Commands  []Entry `json:"commands"`
	Applies   int     `json:"applies"`
	Resources int     `json:"resources"`
}

type Entry struct {
	Hash string    `json:"hash"`
	At   time.Time `json:"at"`
}

// Evaluate registra a atividade e decide. Recebe o relógio como parâmetro.
func (s *State) Evaluate(a Activity, now time.Time) (hook.Decision, string) {
	s.Commands = slices.DeleteFunc(s.Commands, func(e Entry) bool {
		return now.Sub(e.At) >= repeatWindow
	})

	decision, reason := hook.Allow, ""
	if a.Command != "" {
		hash := s.hash(a.Command)
		s.Commands = append(s.Commands, Entry{Hash: hash, At: now})
		count := 0
		for _, e := range s.Commands {
			if e.Hash == hash {
				count++
			}
		}
		switch {
		case count >= denyRepeats:
			return hook.Deny, fmt.Sprintf(repeatDenyReason, count)
		case count >= askRepeats:
			decision, reason = hook.Ask, fmt.Sprintf(repeatAskReason, count)
		}
	}

	if a.Applies > 0 {
		s.Applies += a.Applies
		s.Resources += a.Resources
		if s.Applies > maxApplies || s.Resources > maxResources {
			decision, reason = hook.Ask, joinReasons(reason, fmt.Sprintf(limitReason, s.Applies, s.Resources))
		}
	}
	return decision, reason
}

// hash usa um sal aleatório por sessão: o mesmo comando tem hashes diferentes
// em sessões diferentes, e não há tabela pronta de "hash → comando".
func (s *State) hash(command string) string {
	if s.Salt == "" {
		s.Salt = rand.Text()
	}
	sum := sha256.Sum256([]byte(s.Salt + "\n" + command))
	return hex.EncodeToString(sum[:])
}

func joinReasons(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n\n" + b
}

type Store struct {
	Dir string
}

// DefaultDir é <cache do usuário>/ironbrake/sessions, ou "" se não houver.
func DefaultDir() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "ironbrake", "sessions")
}

// Check registra a atividade e decide. Sem estado legível, pergunta: sem
// memória, não dá para afirmar que não há loop.
func (st Store) Check(sessionID string, a Activity, now time.Time) (hook.Decision, string) {
	if !a.relevant() {
		return hook.Allow, ""
	}
	decision, reason, err := st.update(sessionID, a, now)
	if err != nil {
		return hook.Ask, stateErrorReason
	}
	return decision, reason
}

var errCorrupted = errors.New("estado da sessão corrompido")

// update lê, decide e grava com a trava, para hooks simultâneos não
// perderem contagens.
func (st Store) update(sessionID string, a Activity, now time.Time) (hook.Decision, string, error) {
	if st.Dir == "" {
		return "", "", errors.New("sem pasta de estado")
	}
	if err := os.MkdirAll(st.Dir, 0o700); err != nil {
		return "", "", err
	}
	st.cleanup(now)

	unlock, err := st.lock(sessionID)
	if err != nil {
		return "", "", err
	}
	defer unlock()

	s, err := st.read(sessionID)
	corrupted := errors.Is(err, errCorrupted)
	if err != nil && !corrupted {
		return "", "", err
	}
	if corrupted {
		s = State{}
	}

	decision, reason := s.Evaluate(a, now)
	if err := st.write(sessionID, s); err != nil {
		return "", "", err
	}
	if corrupted && decision == hook.Allow {
		return hook.Ask, stateErrorReason, nil
	}
	return decision, reason, nil
}

// sessionFile é um hash do session_id: usado direto, um "../" no id
// escaparia da pasta.
func sessionFile(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:16])
}

func (st Store) path(sessionID, ext string) string {
	return filepath.Join(st.Dir, sessionFile(sessionID)+ext)
}

func (st Store) lock(sessionID string) (func(), error) {
	return filelock.Acquire(st.path(sessionID, ".lock"), lockTimeout)
}

func (st Store) read(sessionID string) (State, error) {
	data, err := safefile.Read(st.path(sessionID, ".json"), maxStateSize)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if json.Unmarshal(data, &s) != nil {
		return State{}, errCorrupted
	}
	return s, nil
}

// write grava num temporário (0600) e renomeia: o arquivo nunca fica pela
// metade.
func (st Store) write(sessionID string, s State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(st.Dir, sessionFile(sessionID)+".tmp-*")
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
	return os.Rename(tmp.Name(), st.path(sessionID, ".json"))
}

// cleanup apaga sessões paradas há mais de maxAge e temporários esquecidos.
func (st Store) cleanup(now time.Time) {
	entries, err := os.ReadDir(st.Dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= maxAge {
			continue
		}
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".json"):
			os.Remove(filepath.Join(st.Dir, name))
			os.Remove(filepath.Join(st.Dir, strings.TrimSuffix(name, ".json")+".lock"))
		case strings.Contains(name, ".tmp-"):
			os.Remove(filepath.Join(st.Dir, name))
		}
	}
}
