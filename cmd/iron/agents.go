package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/status"
	"golang.org/x/term"
)

// allAgents é o valor de --agent que instala em todos os agentes detectados.
const allAgents = "all"

// parseInitAgents lê os "--agent=a,b" (ou "--agent a,b", repetido) do init. Devolve
// os nomes canônicos na ordem em que apareceram, sem repetir; "all" fica como
// está. Sem a opção, a lista vem vazia (o init decide).
func parseInitAgents(args []string) ([]string, error) {
	var values []string
	for i, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--agent="); ok {
			values = append(values, value)
		} else if arg == "--agent" && i+1 < len(args) {
			values = append(values, args[i+1])
		}
	}

	var names []string
	for _, value := range values {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !strings.EqualFold(name, allAgents) {
				agent, err := hook.Lookup(name)
				if err != nil {
					return nil, err
				}
				name = agent.Name()
			} else {
				name = allAgents
			}
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// isTerminal diz se r é um terminal de verdade (e então dá para perguntar). Só
// olhar se é um dispositivo de caracteres não serve: /dev/null também é.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// initEnv reúne o que o init multiagente lê do mundo externo, para os testes trocarem.
type initEnv struct {
	dir, exePath, home string
	lookPath           func(string) (string, error)
	interactive        bool
	stdin              io.Reader
}

func initHere(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	home, _ := os.UserHomeDir()
	// os.Executable() não normaliza: "../bin/iron" vira ".../a/../bin/iron", e o
	// hook ficaria gravado com esse caminho.
	return runInitAgents(args, initEnv{
		dir: dir, exePath: filepath.Clean(exePath), home: home, lookPath: exec.LookPath,
		interactive: isTerminal(stdin), stdin: stdin,
	}, stdout, stderr)
}

// runInitAgents instala o hook nos agentes pedidos. Sem --agent instala o do
// Claude Code (como sempre) e, se achar outros agentes nesta máquina, pergunta
// (num terminal) ou só avisa o comando (sem terminal).
func runInitAgents(args []string, env initEnv, stdout, stderr io.Writer) int {
	requested, err := parseInitAgents(args)
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 2
	}
	opts := initOptions{harden: slices.Contains(args, "--harden"), awsTag: !slices.Contains(args, "--no-aws-tag")}
	detected := setup.DetectAgents(env.home, env.lookPath)

	names, code := resolveInitAgents(requested, detected, env, stdout, stderr)
	if code != 0 {
		return code
	}
	if opts.harden && !slices.Contains(names, hook.Claude.Name()) {
		fmt.Fprintln(stderr, "iron: --harden só vale para o Claude Code (usa regras de permissão dele); inclua claude em --agent")
		return 2
	}

	worst := 0
	for _, name := range names {
		if len(names) > 1 {
			fmt.Fprintf(stdout, "iron: == %s ==\n", status.Display[name])
		}
		o := opts
		o.harden = opts.harden && name == hook.Claude.Name()
		if c := runInitFor(name, env.dir, env.exePath, o, stdout, stderr); c > worst {
			worst = c
		}
	}
	return worst
}

// resolveInitAgents decide em quais agentes instalar.
func resolveInitAgents(requested []string, detected []setup.Detected, env initEnv, stdout, stderr io.Writer) ([]string, int) {
	if len(requested) > 0 {
		var names []string
		for _, name := range requested {
			if name != allAgents {
				names = appendUnique(names, name)
				continue
			}
			for _, d := range detected {
				names = appendUnique(names, d.Agent)
			}
		}
		if len(names) == 0 {
			fmt.Fprintln(stderr, "iron: --agent=all não achou nenhum agente nesta máquina; use --agent=NOME (claude, kiro, antigravity, codex)")
			return nil, 2
		}
		return names, 0
	}

	// Sem --agent: o Claude Code, como sempre, e os outros só com o seu aval.
	names := []string{hook.Claude.Name()}
	var extras []setup.Detected
	for _, d := range detected {
		if d.Agent != hook.Claude.Name() && !status.Installed(env.dir, d.Agent) {
			extras = append(extras, d)
		}
	}
	if len(extras) == 0 {
		return names, 0
	}

	if !env.interactive {
		var flags []string
		for _, d := range extras {
			flags = append(flags, d.Agent)
		}
		fmt.Fprintf(stdout, "iron: também detectado nesta máquina: %s. Para instalar neles: iron init --agent=%s (ou --agent=all)\n",
			displayList(extras), strings.Join(flags, ","))
		return names, 0
	}

	in := bufio.NewReader(env.stdin)
	for _, d := range extras {
		fmt.Fprintf(stdout, "iron: %s detectado (%s). Instalar o hook do Iron Brake aqui também? [s/N] ", status.Display[d.Agent], d.Evidence)
		line, _ := in.ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer == "s" || answer == "sim" || answer == "y" || answer == "yes" {
			names = append(names, d.Agent)
		}
	}
	return names, 0
}

func appendUnique(list []string, name string) []string {
	if slices.Contains(list, name) {
		return list
	}
	return append(list, name)
}

func displayList(detected []setup.Detected) string {
	names := make([]string, len(detected))
	for i, d := range detected {
		names[i] = status.Display[d.Agent]
	}
	return strings.Join(names, ", ")
}

// runInitFor chama o instalador do agente.
func runInitFor(name, dir, exePath string, opts initOptions, stdout, stderr io.Writer) int {
	switch name {
	case hook.Kiro.Name():
		return runInitKiro(dir, exePath, opts, stdout, stderr)
	case hook.Antigravity.Name():
		return runInitAntigravity(dir, exePath, opts, stdout, stderr)
	case hook.Codex.Name():
		return runInitCodex(dir, exePath, opts, stdout, stderr)
	}
	return runInit(dir, exePath, opts, stdout, stderr)
}

// statusHere mostra a situação de cada agente nesta pasta.
func statusHere(stdout, stderr io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	home, _ := os.UserHomeDir()
	log := audit.Log{Path: audit.DefaultPath()}
	now := time.Now()
	agents := status.Collect(status.Input{
		Dir: dir, Home: home, CodexConfig: codexConfigPath(), LookPath: exec.LookPath,
		Entries: log.Entries, Now: now,
	})
	status.Print(stdout, dir, agents, now)
	return 0
}
