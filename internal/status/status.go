// Package status mostra, por agente, se o Iron Brake está instalado na pasta,
// com o que foi verificado e o que a pessoa precisa saber. Só lê arquivos:
// não executa o hook e não grava no log (isso é do "iron doctor").
package status

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

// Verified é com que versão cada agente foi verificado de verdade.
var Verified = map[string]string{
	hook.Claude.Name():      "Claude Code 2.1.286",
	hook.Kiro.Name():        "Kiro CLI 2.26.1",
	hook.Antigravity.Name(): "Antigravity CLI 1.2.14",
	hook.Codex.Name():       "Codex CLI 0.159.3",
}

// Display é o nome para mostrar.
var Display = map[string]string{
	hook.Claude.Name():      "Claude Code",
	hook.Kiro.Name():        "Kiro CLI",
	hook.Antigravity.Name(): "Antigravity CLI",
	hook.Codex.Name():       "Codex CLI",
}

// Order é a ordem em que os agentes aparecem.
var Order = []string{hook.Claude.Name(), hook.Kiro.Name(), hook.Antigravity.Name(), hook.Codex.Name()}

// LastWindow é quanto o status olha para trás à procura da última decisão.
const LastWindow = 30 * 24 * time.Hour

// Input reúne o que o status lê do mundo externo, para os testes trocarem.
type Input struct {
	Dir         string
	Home        string
	CodexConfig string // ~/.codex/config.toml
	LookPath    func(string) (string, error)
	Entries     func(since time.Time) ([]audit.Entry, error)
	Now         time.Time
}

// Agent é a situação de um agente.
type Agent struct {
	Name      string
	Detected  bool
	Evidence  string
	Installed bool // o hook está instalado nesta pasta

	// Unprotected: instalado, mas o agente não o roda ou ele não funciona (hook
	// não confiado, programa que não existe): o comando passa sem o Iron Brake.
	Unprotected bool
	Where       string
	Warnings    []string
	Last        *audit.Entry // última decisão registrada para o agente
}

// Collect devolve a situação de cada agente, na ordem de Order.
func Collect(in Input) []Agent {
	detected := map[string]string{}
	for _, d := range setup.DetectAgents(in.Home, in.LookPath) {
		detected[d.Agent] = d.Evidence
	}

	var entries []audit.Entry
	if in.Entries != nil {
		entries, _ = in.Entries(in.Now.Add(-LastWindow))
	}

	agents := make([]Agent, 0, len(Order))
	for _, name := range Order {
		a := Agent{Name: name}
		a.Evidence, a.Detected = detected[name]
		switch name {
		case hook.Claude.Name():
			probeClaude(in, &a)
		case hook.Kiro.Name():
			probeKiro(in, &a)
		case hook.Antigravity.Name():
			probeAntigravity(in, &a)
		case hook.Codex.Name():
			probeCodex(in, &a)
		}
		a.Last = lastDecision(entries, name)
		agents = append(agents, a)
	}
	return agents
}

// Installed devolve se o hook de agent está instalado em dir (o que o
// "iron init" usa para não perguntar de novo).
func Installed(dir, agent string) bool {
	for _, a := range Collect(Input{Dir: dir, LookPath: func(string) (string, error) { return "", errors.New("-") }}) {
		if a.Name == agent {
			return a.Installed
		}
	}
	return false
}

func lastDecision(entries []audit.Entry, agent string) *audit.Entry {
	var last *audit.Entry
	for i := range entries {
		e := &entries[i]
		name := e.Agent
		if name == "" {
			name = hook.Claude.Name() // o log da v0.1 não tinha o campo
		}
		if name != agent || e.Rule == "watch" || e.Decision == "gap" {
			continue
		}
		if last == nil || e.Time.After(last.Time) {
			last = e
		}
	}
	return last
}

// rel mostra o caminho relativo à pasta, que é o que a pessoa reconhece.
func rel(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return r
	}
	return path
}

func missingBinary(exe string) string {
	if info, err := os.Stat(exe); err != nil || info.IsDir() {
		return fmt.Sprintf("NÃO PROTEGE: o programa %s não existe: o hook não roda e o comando passa (rode iron init de novo)", exe)
	}
	return ""
}

// warn registra um aviso; um que começa com "NÃO PROTEGE" também marca o agente
// como sem proteção.
func (a *Agent) warn(text string) {
	a.Warnings = append(a.Warnings, text)
	if strings.HasPrefix(text, "NÃO PROTEGE") {
		a.Unprotected = true
	}
}

func probeClaude(in Input, a *Agent) {
	path := setup.SettingsPath(in.Dir)
	hooks, err := setup.FindHooks(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		a.Warnings = append(a.Warnings, err.Error())
		return
	case len(hooks) == 0:
		if renamed, _ := setup.FindRenamedHooks(path); len(renamed) > 0 {
			a.Warnings = append(a.Warnings, "há um hook com o argumento \"hook\" apontando para um programa que não se chama iron; o iron doctor só reconhece iron (ou iron.exe)")
		}
		return
	}
	a.Installed, a.Where = true, rel(in.Dir, path)
	for _, h := range hooks {
		if h.Timeout != 0 && h.Timeout < hook.MinTimeout.Seconds() {
			a.Warnings = append(a.Warnings, fmt.Sprintf("timeout de %g s: o Claude Code desiste antes do prazo do Iron Brake (mínimo %g s) e deixa o comando passar", h.Timeout, hook.MinTimeout.Seconds()))
		}
		if w := missingBinary(h.Command); w != "" {
			a.warn(w)
		}
	}
}

func probeKiro(in Input, a *Agent) {
	found, err := setup.FindKiro(in.Dir)
	if err != nil {
		a.Warnings = append(a.Warnings, err.Error())
		return
	}
	a.Installed = len(found.V2) > 0 || len(found.V3) > 0
	if !a.Installed {
		return
	}
	a.Where = ".kiro/agents (v2) e .kiro/hooks (v3)"
	switch {
	case len(found.V2) == 0:
		a.Warnings = append(a.Warnings, "falta o hook no agente do v2: o Kiro v2 só lê hook de dentro do agente (rode iron init --agent=kiro)")
	case len(found.V3) == 0:
		a.Warnings = append(a.Warnings, "falta o .kiro/hooks/iron-brake.json: o Kiro v3 não lê o hook do agente v2 (rode iron init --agent=kiro)")
	}
	minimum := hook.Kiro.Capabilities().Budget().Deadline.Seconds() + 10
	for _, h := range append(append([]setup.KiroHook{}, found.V2...), found.V3...) {
		switch {
		case h.Timeout == 0:
			a.Warnings = append(a.Warnings, fmt.Sprintf("%s sem prazo: o Kiro libera o comando se o hook demorar (~10 s no v2)", h.File))
		case h.Timeout < minimum:
			a.Warnings = append(a.Warnings, fmt.Sprintf("%s com %g s (mínimo %g s)", h.File, h.Timeout, minimum))
		}
		if w := missingBinary(h.Exe); w != "" {
			a.warn(w)
		}
	}
	for _, f := range found.Doubled {
		a.Warnings = append(a.Warnings, f+" (formato v3) também tem o hook: no v3 ele roda duas vezes por comando")
	}
}

func probeAntigravity(in Input, a *Agent) {
	hooks, err := setup.FindAntigravity(in.Dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		a.Warnings = append(a.Warnings, err.Error()+": o Antigravity ignora um hooks.json inválido SEM avisar, e os comandos passam sem o Iron Brake")
		return
	}
	minimum := hook.Antigravity.Capabilities().Budget().Deadline.Seconds() + 10
	for _, h := range hooks {
		switch {
		case !h.Enabled:
			a.Warnings = append(a.Warnings, fmt.Sprintf("o conjunto %q está com enabled:false", h.Set))
			continue
		case !setup.MatcherCovers(h.Matcher, hook.AntigravityShellTool):
			a.Warnings = append(a.Warnings, fmt.Sprintf("o matcher %q não pega a ferramenta %s", h.Matcher, hook.AntigravityShellTool))
			continue
		}
		a.Installed, a.Where = true, rel(in.Dir, setup.AntigravityHooksPath(in.Dir))
		if h.Timeout < minimum {
			a.Warnings = append(a.Warnings, fmt.Sprintf("prazo de %g s (mínimo %g s): a janela de confirmação seria cortada e o comando, bloqueado", h.Timeout, minimum))
		}
		if w := missingBinary(h.Exe); w != "" {
			a.warn(w)
		}
	}
}

func probeCodex(in Input, a *Agent) {
	hooks, err := setup.FindCodex(in.Dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		a.Warnings = append(a.Warnings, err.Error())
		return
	}
	var usable []setup.CodexHook
	for _, h := range hooks {
		if setup.MatcherCovers(h.Matcher, hook.CodexShellTool) {
			usable = append(usable, h)
		} else {
			a.Warnings = append(a.Warnings, fmt.Sprintf("o matcher %q não pega a ferramenta %s", h.Matcher, hook.CodexShellTool))
		}
	}
	if len(usable) == 0 {
		return
	}
	a.Installed, a.Where = true, rel(in.Dir, setup.CodexHooksPath(in.Dir))

	minimum := hook.Codex.Capabilities().Budget().Deadline.Seconds() + 10
	for _, h := range usable {
		if h.Timeout != 0 && h.Timeout < minimum {
			a.Warnings = append(a.Warnings, fmt.Sprintf("prazo de %g s (mínimo %g s): o Codex mata o hook e LIBERA o comando", h.Timeout, minimum))
		}
		if w := missingBinary(h.Exe); w != "" {
			a.warn(w)
		}
	}

	trust, err := setup.ReadCodexTrust(in.CodexConfig, in.Dir)
	if err != nil {
		a.Warnings = append(a.Warnings, "não consegui ler "+in.CodexConfig+": "+err.Error())
		return
	}
	untrusted := false
	for _, h := range usable {
		untrusted = untrusted || !trust.TrustedHooks[h.Key]
	}
	info, statErr := os.Stat(setup.CodexHooksPath(in.Dir))
	switch {
	case !trust.ProjectTrusted:
		a.warn("NÃO PROTEGE: a pasta não está confiada no Codex, que desativa os hooks do projeto sem avisar no codex exec (abra o codex aqui e confie)")
	case untrusted:
		a.warn("NÃO PROTEGE: o hook ainda não foi confiado no Codex (Review hooks → t, ou /hooks): ele não o roda")
	case statErr == nil && !trust.ConfigModTime.IsZero() && info.ModTime().After(trust.ConfigModTime):
		a.Warnings = append(a.Warnings, "o hooks.json foi alterado depois da última confiança gravada: se o hook mudou, o Codex não o roda até você confiar de novo (/hooks)")
	}
}

// Print escreve a tela do status.
func Print(w io.Writer, dir string, agents []Agent, now time.Time) {
	fmt.Fprintf(w, "iron status: %s\n\n", dir)
	protecting := 0
	for _, a := range agents {
		state := "OK    "
		switch {
		case a.Installed && len(a.Warnings) == 0:
			protecting++
		case a.Installed:
			state = "AVISO "
			if !a.Unprotected {
				protecting++
			}
		case len(a.Warnings) > 0:
			state = "AVISO "
		default:
			state = "      "
		}
		fmt.Fprintf(w, "%s %-16s %s\n", state, Display[a.Name], summary(a))
		if a.Installed {
			fmt.Fprintf(w, "       %-16s verificado com %s\n", "", Verified[a.Name])
			fmt.Fprintf(w, "       %-16s %s\n", "", lastLine(a.Last, now))
		}
		for _, warning := range a.Warnings {
			fmt.Fprintf(w, "       %-16s ! %s\n", "", warning)
		}
	}
	fmt.Fprintf(w, "\nProtegendo esta pasta: %d de %d agente(s). Para conferir de verdade (executa o hook): iron doctor --agent=NOME\n", protecting, len(agents))
}

func summary(a Agent) string {
	switch {
	case a.Installed:
		return "instalado em " + a.Where
	case a.Detected:
		return "detectado nesta máquina, não instalado aqui: iron init --agent=" + a.Name
	}
	return "não detectado nesta máquina"
}

func lastLine(e *audit.Entry, now time.Time) string {
	if e == nil {
		return "última decisão: nenhuma nos últimos 30 dias"
	}
	text := fmt.Sprintf("última decisão: %s, %s → %s", e.Time.Local().Format("02/01 15:04"), e.Class, e.Decision)
	if e.Rule != "" {
		text += " (" + e.Rule + ")"
	}
	return text
}
