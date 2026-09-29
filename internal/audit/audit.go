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
	"slices"
	"strconv"
	"strings"
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

// Rotação: passando de MaxSize, o log vira Path.1 (o antigo Path.1 vira Path.2
// e assim por diante) e sobram só Keep arquivos rotacionados. Com o padrão, o
// disco fica em no máximo ~30 MB (cerca de 30 mil decisões por arquivo).
// Padrões, exportados para o teste conferir que combinam com o policy.
const (
	DefaultMaxSize = 5 << 20
	DefaultKeep    = 5
)

type Log struct {
	Path    string
	MaxSize int64 // 0 = padrão (5 MB)
	Keep    int   // arquivos rotacionados guardados; 0 = padrão (5)
}

func (l Log) maxSize() int64 {
	if l.MaxSize > 0 {
		return l.MaxSize
	}
	return DefaultMaxSize
}

func (l Log) keep() int {
	if l.Keep > 0 {
		return l.Keep
	}
	return DefaultKeep
}

// rotated é o caminho do arquivo rotacionado número n (1 = o mais recente).
func (l Log) rotated(n int) string { return fmt.Sprintf("%s.%d", l.Path, n) }

// anchorPath guarda o hash da última linha do arquivo mais antigo que já foi
// apagado: é o que a primeira linha do arquivo mais antigo que sobrou deve
// apontar, para a corrente continuar conferível depois da poda.
func (l Log) anchorPath() string { return l.Path + ".anchor" }

// failurePath marca que a última gravação falhou (some na próxima que der certo).
func (l Log) failurePath() string { return l.Path + ".error" }

// DefaultPath é ~/.iron/audit.log. Vazio se não houver pasta do usuário.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".iron", "audit.log")
}

// Append grava a entrada no fim do log, encadeada na última linha, com a
// trava do log para hooks simultâneos não lerem a mesma "última linha". Se a
// gravação falha, deixa um marcador que o iron doctor mostra (o stderr do
// hook só aparece para o usuário quando o comando é bloqueado).
func (l Log) Append(e Entry) error {
	err := l.append(e)
	l.trackFailure(err)
	return err
}

func (l Log) append(e Entry) error {
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
	defer func() { f.Close() }()

	last, endsWithNewline, err := lastLine(f)
	if err != nil {
		return err
	}

	// Log cheio: rotaciona. A primeira linha do arquivo novo aponta para a
	// última do que acabou de sair, então a corrente não quebra.
	if info, statErr := f.Stat(); statErr == nil && info.Size() >= l.maxSize() {
		f.Close() // no Windows não dá para renomear um arquivo aberto
		if err := l.rotate(); err != nil {
			return err
		}
		f, err = os.OpenFile(l.Path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		endsWithNewline = true
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

// rotatedNumbers lista os números dos arquivos Path.N que existem, do menor
// (mais novo) ao maior. Achar pelos nomes, e não por Keep, deixa o Verify
// funcionar com qualquer configuração, inclusive uma que já mudou.
func (l Log) rotatedNumbers() []int {
	matches, err := filepath.Glob(l.Path + ".*")
	if err != nil {
		return nil
	}
	var nums []int
	for _, m := range matches {
		suffix := strings.TrimPrefix(m, l.Path+".")
		if n, err := strconv.Atoi(suffix); err == nil && n > 0 && strconv.Itoa(n) == suffix {
			nums = append(nums, n)
		}
	}
	slices.Sort(nums)
	return nums
}

// rotate desloca Path.N → Path.N+1 e Path → Path.1, e apaga o que passar de
// Keep. Antes de apagar, guarda a âncora: o hash da última linha do mais novo
// dos apagados, que é o que a primeira linha do mais antigo que ficou aponta.
func (l Log) rotate() error {
	nums := l.rotatedNumbers()
	for i := len(nums) - 1; i >= 0; i-- {
		if err := os.Rename(l.rotated(nums[i]), l.rotated(nums[i]+1)); err != nil {
			return err
		}
	}
	if err := os.Rename(l.Path, l.rotated(1)); err != nil {
		return err
	}

	var doomed []int // do mais novo ao mais antigo
	for _, n := range l.rotatedNumbers() {
		if n > l.keep() {
			doomed = append(doomed, n)
		}
	}
	if len(doomed) == 0 {
		return nil
	}
	if last, err := lastLineOf(l.rotated(doomed[0])); err == nil && last != nil {
		if err := writeAtomic(l.anchorPath(), []byte(lineHash(last)+"\n")); err != nil {
			return err
		}
	}
	for i := len(doomed) - 1; i >= 0; i-- {
		if err := os.Remove(l.rotated(doomed[i])); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func lastLineOf(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	last, _, err := lastLine(f)
	return last, err
}

// writeAtomic grava num temporário (0600) e renomeia.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
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
	return os.Rename(tmp.Name(), path)
}

// Failure é a última gravação que falhou: quando e por quê (nunca o comando).
type Failure struct {
	Time   time.Time `json:"time"`
	Reason string    `json:"reason"`
}

const maxFailureReason = 200

// trackFailure grava ou apaga o marcador de falha. É o melhor esforço: se a
// própria pasta do log não aceita gravação, o marcador também não entra, e a
// verificação de escrita do iron doctor pega o problema.
func (l Log) trackFailure(err error) {
	if l.Path == "" {
		return
	}
	if err == nil {
		os.Remove(l.failurePath())
		return
	}
	reason := strings.ToValidUTF8(err.Error(), "?")
	if len(reason) > maxFailureReason {
		reason = reason[:maxFailureReason]
	}
	if data, marshalErr := json.Marshal(Failure{Time: time.Now(), Reason: reason}); marshalErr == nil {
		os.WriteFile(l.failurePath(), data, 0o600)
	}
}

// Health é o que o iron doctor confere no log.
type Health struct {
	Chain       Result   // corrente dos arquivos (Verify)
	WriteErr    error    // não dá para gravar agora
	LastFailure *Failure // a última gravação do hook falhou (e nenhuma deu certo depois)
}

// Check confere se o log pode ser gravado, se a corrente está íntegra e se a
// última gravação do hook falhou. Não acrescenta linha nenhuma.
func (l Log) Check() Health {
	h := Health{Chain: l.Verify(), WriteErr: l.checkWritable()}
	if data, err := os.ReadFile(l.failurePath()); err == nil {
		var f Failure
		if json.Unmarshal(data, &f) == nil {
			h.LastFailure = &f
		}
	}
	return h
}

// checkWritable faz o que o Append faz antes de gravar: pasta, trava e
// abertura do arquivo para escrita.
func (l Log) checkWritable() error {
	if l.Path == "" {
		return errors.New("sem caminho para o log de auditoria")
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	release, err := filelock.Acquire(l.Path+".lock", lockTimeout)
	if err != nil {
		return err
	}
	defer release()

	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
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
	Lines    int    // linhas conferidas até o fim ou até a quebra (somando os arquivos)
	Files    int    // arquivos conferidos (o log atual e os rotacionados)
	Bytes    int64  // tamanho somado dos arquivos
	BrokenAt int    // número da linha (a partir de 1, somando os arquivos) onde a corrente quebrou
	File     string // arquivo onde a corrente quebrou
	Reason   string // por que quebrou
	Missing  bool   // o arquivo não existe
}

// files lista os arquivos do log do mais antigo para o mais novo.
func (l Log) files() []string {
	var paths []string
	nums := l.rotatedNumbers()
	for i := len(nums) - 1; i >= 0; i-- {
		paths = append(paths, l.rotated(nums[i]))
	}
	if _, err := os.Stat(l.Path); err == nil {
		paths = append(paths, l.Path)
	}
	return paths
}

// Verify percorre o log (do arquivo rotacionado mais antigo ao atual)
// conferindo que cada linha aponta para a anterior.
func (l Log) Verify() Result {
	if len(l.files()) == 0 {
		return Result{OK: true, Missing: true}
	}
	release, err := filelock.Acquire(l.Path+".lock", lockTimeout)
	if err != nil {
		return Result{Reason: "o log está travado por outro processo; tente de novo"}
	}
	defer release()

	// A primeira linha do que sobrou aponta para a âncora (se algo já foi
	// apagado pela rotação) ou para nada (se o log nunca foi podado).
	want, anchored := "", false
	if data, err := os.ReadFile(l.anchorPath()); err == nil {
		want, anchored = strings.TrimSpace(string(data)), true
	}
	total := Result{OK: true}
	first := true
	for _, path := range l.files() {
		f, err := os.Open(path)
		if err != nil {
			total.OK, total.Reason = false, fmt.Sprintf("não consegui abrir o log: %v", err)
			return total
		}
		if info, err := f.Stat(); err == nil {
			total.Bytes += info.Size()
		}
		total.Files++

		reader := bufio.NewReader(f)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) == 0 && errors.Is(err, io.EOF) {
				break
			}
			if err != nil && !errors.Is(err, io.EOF) {
				f.Close()
				total.OK, total.Reason = false, fmt.Sprintf("erro lendo o log: %v", err)
				return total
			}
			line = bytes.TrimSuffix(line, []byte("\n"))
			n := total.Lines + 1

			var e Entry
			if json.Unmarshal(line, &e) != nil {
				f.Close()
				return Result{Lines: total.Lines, Files: total.Files, Bytes: total.Bytes, BrokenAt: n, File: filepath.Base(path), Reason: "a linha não é JSON válido"}
			}
			if e.Prev != want {
				f.Close()
				reason := "a linha não aponta para a anterior: a linha anterior foi alterada, ou uma linha entre elas foi removida"
				switch {
				case first && anchored:
					reason = "a primeira linha não aponta para a âncora da poda (" + filepath.Base(l.anchorPath()) + "): o arquivo mais antigo ou a âncora foram alterados"
				case first:
					reason = "a primeira linha deveria começar a corrente (prev vazio): linhas do começo foram removidas"
				}
				return Result{Lines: total.Lines, Files: total.Files, Bytes: total.Bytes, BrokenAt: n, File: filepath.Base(path), Reason: reason}
			}
			want = lineHash(line)
			total.Lines++
			first = false
		}
		f.Close()
	}
	return total
}
