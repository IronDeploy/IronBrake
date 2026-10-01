package awsgate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Proc é um processo da lista do sistema.
type Proc struct {
	PID, PPID int
	TTY       string // terminal de controle; vazio, "?" ou "??" se não tem
	Command   string // linha de comando completa
}

// HasTerminal diz se o processo tem terminal de controle.
func (p Proc) HasTerminal() bool {
	return p.TTY != "" && p.TTY != "?" && p.TTY != "??" && p.TTY != "-"
}

// ErrUnsupported: o sistema não tem a lista de processos que o portão usa.
var ErrUnsupported = errors.New("o portão de credenciais só funciona no macOS e no Linux")

const maxDepth = 64

// ParsePS lê a saída de "ps -axo pid=,ppid=,tty=,command=".
func ParsePS(output string) map[int]Proc {
	procs := map[int]Proc{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		// O comando é o resto da linha, com os espaços originais.
		rest := strings.TrimSpace(scanner.Text())
		for _, field := range fields[:3] {
			rest = strings.TrimSpace(strings.TrimPrefix(rest, field))
		}
		procs[pid] = Proc{PID: pid, PPID: ppid, TTY: fields[2], Command: rest}
	}
	return procs
}

// Chain devolve o processo start e todos os seus ancestrais, do mais próximo ao
// mais distante. Para no processo 1, num processo desconhecido ou num ciclo.
func Chain(procs map[int]Proc, start int) []Proc {
	var chain []Proc
	seen := map[int]bool{}
	for pid := start; pid > 1 && !seen[pid] && len(chain) < maxDepth; {
		p, ok := procs[pid]
		if !ok {
			break
		}
		seen[pid] = true
		chain = append(chain, p)
		pid = p.PPID
	}
	return chain
}

// psPaths: caminhos absolutos; um "ps" falso no PATH esconderia o agente.
var psPaths = []string{"/bin/ps", "/usr/bin/ps"}

// ListProcesses lê os processos do sistema.
func ListProcesses() (map[int]Proc, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, ErrUnsupported
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var lastErr error
	for _, path := range psPaths {
		out, err := exec.CommandContext(ctx, path, "-axo", "pid=,ppid=,tty=,command=").Output()
		if err == nil {
			return ParsePS(string(out)), nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("não consegui listar os processos: %w", lastErr)
}

// HasTerminal diz se algum processo da cadeia tem terminal de controle: um
// humano num terminal sempre tem; um processo desligado do pai (daemon,
// setsid), um cron e um CI não.
func HasTerminal(chain []Proc) bool {
	for _, p := range chain {
		if p.HasTerminal() {
			return true
		}
	}
	return false
}
