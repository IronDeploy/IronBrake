package audit

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/IronDeploy/IronBrake/internal/filelock"
)

const lockTimeout = 2 * time.Second

// tailSize: basta ler o fim do log para achar a última linha.
const tailSize = 64 * 1024

// Entry é uma linha do log. Não existe campo para o comando, de propósito.
type Entry struct {
	Time      time.Time  `json:"time"`
	Session   string     `json:"session"`
	Class     string     `json:"class"`               // ex.: "terraform apply", "git push", "outro"
	Decision  string     `json:"decision"`            // allow, ask, deny ou userApproved
	Rule      string     `json:"rule,omitempty"`      // regra(s) que decidiram, ex.: "git-force-push"
	Dialog    string     `json:"dialog,omitempty"`    // resposta da janela: approved, rejected, unavailable
	Resources []Resource `json:"resources,omitempty"` // recursos que um terraform apply altera
	Prev      string     `json:"prev"`                // hash da linha anterior ("" na primeira)
}

type Resource struct {
	Address string `json:"address"`
	Action  string `json:"action"`
}

type Log struct {
	Path string
}

// DefaultPath é ~/.iron/audit.log. Vazio se não houver pasta do usuário.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".iron", "audit.log")
}

// Append grava a entrada no fim do log, encadeada na última linha, com a
// trava do log para hooks simultâneos não lerem a mesma "última linha".
func (l Log) Append(e Entry) error {
	if l.Path == "" {
		return errors.New("sem caminho para o log de auditoria")
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}

	// Trava num arquivo ao lado: no Windows, o arquivo travado fica inacessível.
	release, err := filelock.Acquire(l.Path+".lock", lockTimeout)
	if err != nil {
		return err
	}
	defer release()

	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	last, endsWithNewline, err := lastLine(f)
	if err != nil {
		return err
	}
	e.Prev = ""
	if last != nil {
		e.Prev = lineHash(last)
	}

	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if !endsWithNewline {
		// Última linha incompleta: começa uma nova; o Verify vai apontar a quebrada.
		data = append([]byte("\n"), data...)
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// lastLine devolve a última linha (nil se o arquivo está vazio) e se o
// arquivo termina em "\n".
func lastLine(f *os.File) ([]byte, bool, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	size := info.Size()
	if size == 0 {
		return nil, true, nil
	}

	start := max(0, size-tailSize)
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}

	endsWithNewline := buf[len(buf)-1] == '\n'
	content := bytes.TrimSuffix(buf, []byte("\n"))
	if i := bytes.LastIndexByte(content, '\n'); i >= 0 {
		return content[i+1:], endsWithNewline, nil
	}
	if start == 0 {
		return content, endsWithNewline, nil
	}

	all := make([]byte, size)
	if _, err := f.ReadAt(all, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	content = bytes.TrimSuffix(all, []byte("\n"))
	return content[bytes.LastIndexByte(content, '\n')+1:], endsWithNewline, nil
}

func lineHash(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}

type Result struct {
	OK       bool
	Lines    int    // linhas conferidas até o fim ou até a quebra
	BrokenAt int    // número da linha (a partir de 1) onde a corrente quebrou
	Reason   string // por que quebrou
	Missing  bool   // o arquivo não existe
}

// Verify percorre o log conferindo que cada linha aponta para a anterior.
func (l Log) Verify() Result {
	if _, err := os.Stat(l.Path); errors.Is(err, fs.ErrNotExist) {
		return Result{OK: true, Missing: true}
	}
	release, err := filelock.Acquire(l.Path+".lock", lockTimeout)
	if err != nil {
		return Result{Reason: "o log está travado por outro processo; tente de novo"}
	}
	defer release()

	f, err := os.Open(l.Path)
	if err != nil {
		return Result{Reason: fmt.Sprintf("não consegui abrir o log: %v", err)}
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	var prev []byte
	for n := 1; ; n++ {
		line, err := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(err, io.EOF) {
			return Result{OK: true, Lines: n - 1}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return Result{Lines: n - 1, Reason: fmt.Sprintf("erro lendo o log: %v", err)}
		}
		line = bytes.TrimSuffix(line, []byte("\n"))

		var e Entry
		if json.Unmarshal(line, &e) != nil {
			return Result{Lines: n - 1, BrokenAt: n, Reason: "a linha não é JSON válido"}
		}
		want := ""
		if prev != nil {
			want = lineHash(prev)
		}
		if e.Prev != want {
			reason := "a linha não aponta para a anterior: a linha anterior foi alterada, ou uma linha entre elas foi removida"
			if n == 1 {
				reason = "a primeira linha deveria começar a corrente (prev vazio): linhas do começo foram removidas"
			}
			return Result{Lines: n - 1, BrokenAt: n, Reason: reason}
		}
		prev = line
	}
}
