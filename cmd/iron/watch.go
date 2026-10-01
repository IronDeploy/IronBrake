package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/dialog"
	"github.com/IronDeploy/IronBrake/internal/rules"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/watch"
)

const watchUsage = `uso: iron watch [opções]

  (sem opções)   acompanha os transcripts do Claude Code desta pasta e avisa quando
                 um comando rodou sem decisão do Iron Brake (Ctrl+C para sair)
  --once         confere o que já aconteceu e sai (código 1 se houver lacuna)
  --since DUR    com --once, o período conferido (padrão 1h; ex.: 30m, 24h)
  --dir PASTA    pasta de transcripts (padrão: ~/.claude/projects/<esta pasta>)
  --all          todas as pastas de ~/.claude/projects
  --notify       também avisa por notificação do sistema (macOS)
  --grace DUR    espera depois do resultado antes de acusar (padrão 3s)`

// maxListedGaps limita a lista do --once: com o hook desligado seriam centenas
// de linhas iguais.
const maxListedGaps = 15

type watchOptions struct {
	once   bool
	since  time.Duration
	dir    string
	all    bool
	notify bool
	grace  time.Duration
}

func parseWatchArgs(args []string) (watchOptions, error) {
	opts := watchOptions{since: time.Hour, grace: watch.DefaultGrace}
	duration := func(flag, value string) (time.Duration, error) {
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("valor inválido em %s: %q", flag, value)
		}
		return d, nil
	}

	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		next := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s precisa de um valor", name)
			}
			i++
			return args[i], nil
		}

		var err error
		switch name {
		case "--once":
			opts.once = true
		case "--all":
			opts.all = true
		case "--notify":
			opts.notify = true
		case "--since", "--grace":
			var v string
			if v, err = next(); err == nil {
				var d time.Duration
				if d, err = duration(name, v); err == nil {
					if name == "--since" {
						opts.since = d
					} else {
						opts.grace = d
					}
				}
			}
		case "--dir":
			opts.dir, err = next()
		default:
			err = fmt.Errorf("opção desconhecida %q", args[i])
		}
		if err != nil {
			return opts, err
		}
	}
	return opts, nil
}

// watchDeps reúne o que o watch usa do mundo externo, para os testes trocarem.
type watchDeps struct {
	ctx     context.Context
	home    string
	cwd     string
	now     func() time.Time
	poll    time.Duration
	entries func(since time.Time) ([]audit.Entry, error)
	record  func(audit.Entry) error
	notify  func(title, message string)
}

func runWatchCommand(args []string, stdout, stderr io.Writer) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := audit.Log{Path: audit.DefaultPath()}
	return runWatch(args, stdout, stderr, watchDeps{
		ctx: ctx, home: home, cwd: cwd, now: time.Now, poll: time.Second,
		entries: log.Entries,
		record:  func(e audit.Entry) error { return writeAudit(e, projectAuditConfig()) },
		notify:  dialog.Notify,
	})
}

func runWatch(args []string, stdout, stderr io.Writer, deps watchDeps) int {
	opts, err := parseWatchArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n\n%s\n", err, watchUsage)
		return 2
	}

	dirs, err := watchDirs(opts, deps)
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	if !opts.all && opts.dir == "" {
		warnIfHookMissing(deps.cwd, deps.home, stderr)
	}

	src := watch.Source{Classify: rules.Classify, Entries: deps.entries}
	if opts.once {
		return watchOnce(opts, dirs, src, deps, stdout, stderr)
	}
	return watchFollow(opts, dirs, src, deps, stdout, stderr)
}

func watchDirs(opts watchOptions, deps watchDeps) ([]string, error) {
	switch {
	case opts.dir != "":
		return []string{opts.dir}, nil
	case opts.all:
		root := filepath.Join(deps.home, ".claude", "projects")
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("não achei %s: %w", root, err)
		}
		var dirs []string
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
		return dirs, nil
	}

	dir := watch.TranscriptDir(deps.home, deps.cwd)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("não achei transcripts do Claude Code para esta pasta (%s); use --dir ou --all", dir)
	}
	return []string{dir}, nil
}

// warnIfHookMissing: sem o hook instalado (nem na pasta, nem no usuário), todo
// comando seria acusado; dizer isso logo evita um relatório confuso.
func warnIfHookMissing(cwd, home string, stderr io.Writer) {
	project := setup.SettingsPath(cwd)
	for _, path := range []string{project, setup.SettingsPath(home)} {
		if hooks, err := setup.FindHooks(path); err == nil && len(hooks) > 0 {
			return
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return // arquivo ilegível: não dá para afirmar nada
		}
	}
	fmt.Fprintf(stderr, "iron: aviso: o hook do Iron Brake não está instalado em %s nem no usuário; todo comando será acusado. rode \"iron init\".\n", project)
}

func watchOnce(opts watchOptions, dirs []string, src watch.Source, deps watchDeps, stdout, stderr io.Writer) int {
	gaps, checked, covered, err := watch.Scan(dirs, src, deps.now().Add(-opts.since))
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}

	for i, g := range gaps {
		if i == maxListedGaps {
			fmt.Fprintf(stdout, "iron watch: ... e mais %d.\n", len(gaps)-maxListedGaps)
			break
		}
		fmt.Fprintln(stdout, gapLine(g))
	}
	switch {
	case checked == 0:
		fmt.Fprintf(stdout, "iron: nenhum comando de shell nos últimos %s.\n", opts.since)
		return 0
	case len(gaps) == 0:
		fmt.Fprintf(stdout, "iron: %d comando(s) de shell nos últimos %s, todos com decisão do Iron Brake.\n", checked, opts.since)
		return 0
	}
	fmt.Fprintf(stdout, "iron: %d comando(s) de shell nos últimos %s: %d com decisão, %d SEM decisão.\n", checked, opts.since, covered, len(gaps))
	if covered == 0 {
		fmt.Fprintln(stdout, "      nenhum foi visto pelo Iron Brake: o hook não está instalado ou não dispara neste agente.")
	}
	return 1
}

func watchFollow(opts watchOptions, dirs []string, src watch.Source, deps watchDeps, stdout, stderr io.Writer) int {
	w := watch.New(dirs, src, opts.grace, deps.now())
	fmt.Fprintf(stdout, "iron watch: acompanhando %d pasta(s) de transcripts. Ctrl+C para sair.\n", len(dirs))

	total := 0
	ticker := time.NewTicker(deps.poll)
	defer ticker.Stop()

	for {
		select {
		case <-deps.ctx.Done():
			checked, covered := w.Stats()
			fmt.Fprintf(stdout, "iron watch: %d comando(s) conferido(s), %d com decisão, %d sem.\n", checked, covered, total)
			return 0
		case <-ticker.C:
		}

		gaps, err := w.Poll(deps.now())
		if err != nil {
			fmt.Fprintf(stderr, "iron: %v\n", err)
			continue
		}
		for _, g := range gaps {
			total++
			fmt.Fprintln(stdout, gapLine(g))
			entry := audit.Entry{Session: g.Call.Session, Agent: "claude", Class: g.Class, Decision: "gap", Rule: "watch"}
			if err := deps.record(entry); err != nil {
				fmt.Fprintln(stderr, "iron: não consegui gravar o log de auditoria")
			}
			if opts.notify {
				deps.notify("Iron Brake: comando sem decisão", fmt.Sprintf("%s rodou sem passar pelo hook (sessão %s).", g.Class, shortSession(g.Call.Session)))
			}
		}
	}
}

// gapLine nunca mostra o comando, só a classe, como o log de auditoria.
func gapLine(g watch.Gap) string {
	return fmt.Sprintf("iron watch: SEM DECISÃO: %s às %s (sessão %s). o hook pode não ter rodado.",
		g.Class, g.Call.At.Local().Format("15:04:05"), shortSession(g.Call.Session))
}

func shortSession(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "?"
	}
	return id
}
