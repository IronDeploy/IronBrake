package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/dialog"
	"github.com/IronDeploy/IronBrake/internal/doctor"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/policy"
	"github.com/IronDeploy/IronBrake/internal/rules"
	"github.com/IronDeploy/IronBrake/internal/runenv"
	"github.com/IronDeploy/IronBrake/internal/safefile"
	"github.com/IronDeploy/IronBrake/internal/session"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/tfplan"
)

// version é definida na compilação com -ldflags "-X main.version=...".
var version = "dev"

const (
	approvedByUserReason = "Iron Brake: aprovado pelo usuário na janela de confirmação."
	rejectedByUserReason = "Iron Brake: o usuário recusou este comando na janela de confirmação. não tente de novo; siga com o resto da tarefa ou pergunte ao usuário o que fazer."
	timeoutReason        = "Iron Brake: a análise passou do tempo limite; bloqueado por segurança. tente de novo ou peça ao usuário para executar."
)

const doctorTimeout = 5 * time.Second

const usage = `uso: iron <subcomando>

  hook          roda como hook PreToolUse do Claude Code (lê o evento pelo stdin)
  init          instala o hook em .claude/settings.json da pasta atual
  doctor        verifica se o hook desta pasta está mesmo protegendo
  audit verify  confere se a corrente do log de auditoria (~/.iron/audit.log) está íntegra
  version       mostra a versão`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}

	switch args[0] {
	case "hook":
		return runHook(stdin, stdout, stderr, hookDeps{
			readPlan: tfplan.Show, confirm: dialog.Confirm, loadEnv: loadEnv, session: checkSession, audit: writeAudit,
			deadline: hook.Deadline,
		})
	case "init":
		return initHere(stdout, stderr)
	case "doctor":
		return doctorHere(stdout, stderr)
	case "audit":
		return runAudit(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "iron %s\n", version)
		return 0
	default:
		fmt.Fprintf(stderr, "iron: subcomando desconhecido %q\n\n%s\n", args[0], usage)
		return 2
	}
}

// hookDeps reúne o que o hook usa do mundo externo, para os testes trocarem.
type hookDeps struct {
	readPlan rules.PlanReader
	confirm  func(string) dialog.Answer
	loadEnv  func(cwd string) rules.Env
	session  func(sessionID string, a session.Activity) (hook.Decision, string)
	audit    func(audit.Entry) error
	deadline time.Duration
}

type verdict struct {
	decision hook.Decision
	reason   string
	rule     string
	dialog   string
}

// runHook responde deny se a análise passar do prazo: um hook que estoura o
// timeout do Claude Code deixa o comando passar.
func runHook(stdin io.Reader, stdout, stderr io.Writer, deps hookDeps) int {
	type outcome struct {
		v        verdict
		auditErr error
	}
	done := make(chan outcome, 1)

	go func() {
		var event hook.PreToolUseEvent
		if err := json.NewDecoder(stdin).Decode(&event); err != nil {
			v := verdict{decision: hook.Deny, reason: "iron: não consegui ler o evento do stdin", rule: "event"}
			done <- outcome{v, record(deps, audit.Entry{Class: "evento ilegível"}, v)}
			return
		}
		v, changes := decide(event, deps)
		entry := audit.Entry{Session: event.SessionID, Class: classOf(event), Resources: changes}
		done <- outcome{v, record(deps, entry, v)}
	}()

	select {
	case o := <-done:
		code := hook.Respond(stdout, stderr, o.v.decision, o.v.reason)
		if o.auditErr != nil {
			fmt.Fprintln(stderr, "iron: não consegui gravar o log de auditoria")
		}
		return code
	case <-time.After(deps.deadline):
		return hook.Respond(stdout, stderr, hook.Deny, timeoutReason)
	}
}

func decide(event hook.PreToolUseEvent, deps hookDeps) (verdict, []audit.Resource) {
	command := event.ToolInput.Command
	env := deps.loadEnv(event.Cwd)

	checked := rules.Evaluate(command, env)
	v := verdict{decision: checked.Decision, reason: checked.Reason, rule: checked.Rule}
	if v.decision == hook.Deny {
		return v, nil
	}

	apply := rules.InspectTerraformApply(command, event.Cwd, env, deps.readPlan)
	changes := auditResources(apply.Changes)
	v = combine(v, verdict{decision: apply.Decision, reason: apply.Reason, rule: ruleName(apply.Decision, rules.TerraformApplyRule)})
	if v.decision == hook.Deny {
		return v, changes
	}

	if event.SessionID != "" {
		activity := session.Activity{Applies: apply.Applies, Resources: apply.Resources}
		if rules.TouchesInfra(command) {
			activity.Command = rules.Normalize(command)
		}
		d, why := deps.session(event.SessionID, activity)
		v = combine(v, verdict{decision: d, reason: why, rule: ruleName(d, "session")})
	}

	if v.decision == hook.Ask {
		v = confirmAsk(command, v, deps.confirm)
	}
	return v, changes
}

// record grava a decisão no log. Uma falha não muda a decisão.
func record(deps hookDeps, entry audit.Entry, v verdict) error {
	entry.Decision = string(v.decision)
	entry.Rule = v.rule
	entry.Dialog = v.dialog
	return deps.audit(entry)
}

func classOf(event hook.PreToolUseEvent) string {
	if event.ToolName != "" && event.ToolName != "Bash" {
		return "ferramenta " + event.ToolName
	}
	if class := rules.Classify(event.ToolInput.Command); class != "" {
		return class
	}
	return "vazio"
}

func ruleName(decision hook.Decision, name string) string {
	if decision == hook.Allow {
		return ""
	}
	return name
}

func auditResources(changes []tfplan.Resource) []audit.Resource {
	var result []audit.Resource
	for _, r := range changes {
		result = append(result, audit.Resource{Address: r.Address, Action: r.Action})
	}
	return result
}

// combine: deny vence; dois asks somam motivos e regras.
func combine(a, b verdict) verdict {
	switch {
	case a.decision == hook.Deny:
		return a
	case b.decision == hook.Deny:
		return b
	case a.decision == hook.Ask && b.decision == hook.Ask:
		return verdict{decision: hook.Ask, reason: a.reason + "\n\n" + b.reason, rule: a.rule + "+" + b.rule}
	case b.decision == hook.Ask:
		return b
	}
	return a
}

func checkSession(sessionID string, a session.Activity) (hook.Decision, string) {
	return session.Store{Dir: session.DefaultDir()}.Check(sessionID, a, time.Now())
}

func writeAudit(e audit.Entry) error {
	e.Time = time.Now()
	return audit.Log{Path: audit.DefaultPath()}.Append(e)
}

// loadEnv usa a política de CLAUDE_PROJECT_DIR (ou da pasta do comando).
// Política com erro vira PolicyError, e as regras tratam tudo como produção.
func loadEnv(cwd string) rules.Env {
	projectDir := os.Getenv("CLAUDE_PROJECT_DIR")
	if projectDir == "" {
		projectDir = cwd
	}

	p, err := policy.Load(projectDir)
	return rules.Env{
		Policy:      p,
		Context:     runenv.Collect(cwd, os.Getenv, readContextFile),
		PolicyError: err != nil,
		Cwd:         cwd,
		ReadFile:    readContextFile,
	}
}

func readContextFile(path string) ([]byte, error) {
	return safefile.Read(path, 4<<20)
}

// confirmAsk mostra o motivo numa janela do sistema, porque a caixa do Claude
// Code não o mostra antes da decisão.
func confirmAsk(command string, v verdict, confirm func(string) dialog.Answer) verdict {
	answer := confirm(v.reason)
	v.dialog = string(answer)

	switch answer {
	case dialog.Approved:
		// O allow explícito vale para a linha inteira, mas a janela só mostrou
		// este motivo: com mais comandos na linha, o Claude Code decide.
		if !rules.IsSingleCommand(command) {
			v.decision, v.reason = hook.Allow, ""
			return v
		}
		v.decision, v.reason = hook.UserApproved, approvedByUserReason
	case dialog.Rejected:
		v.decision, v.reason = hook.Deny, rejectedByUserReason+"\n\n"+v.reason
	}
	return v
}

func runAudit(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "verify" {
		fmt.Fprintln(stderr, "uso: iron audit verify")
		return 2
	}
	log := audit.Log{Path: audit.DefaultPath()}
	r := log.Verify()

	switch {
	case r.Missing:
		fmt.Fprintf(stdout, "iron: %s não existe: nenhuma decisão registrada ainda.\n", log.Path)
		return 0
	case r.OK:
		fmt.Fprintf(stdout, "iron: corrente íntegra: %d linha(s) conferida(s) em %s.\n", r.Lines, log.Path)
		fmt.Fprintln(stdout, "      (a corrente não detecta se o arquivo inteiro ou as últimas linhas foram apagados.)")
		return 0
	case r.BrokenAt > 0:
		fmt.Fprintf(stdout, "iron: corrente QUEBRADA na linha %d de %s: %s.\n", r.BrokenAt, log.Path, r.Reason)
		// A linha N confirma a N-1: se N não bate, a suspeita é a N-1.
		if confirmed := r.BrokenAt - 2; confirmed > 0 {
			fmt.Fprintf(stdout, "      confirmadas: linhas 1 a %d. suspeita: linha %d (ou uma linha removida logo depois dela).\n", confirmed, r.BrokenAt-1)
		}
		return 1
	default:
		fmt.Fprintf(stdout, "iron: não consegui verificar %s: %s.\n", log.Path, r.Reason)
		return 1
	}
}

func initHere(stdout, stderr io.Writer) int {
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
	return runInit(dir, exePath, stdout, stderr)
}

func runInit(dir, exePath string, stdout, stderr io.Writer) int {
	settingsPath := setup.SettingsPath(dir)

	changed, err := setup.InstallHook(settingsPath, exePath)
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}

	if changed {
		fmt.Fprintf(stdout, "iron: hook instalado em %s\n", settingsPath)
	} else {
		fmt.Fprintf(stdout, "iron: o hook já estava instalado em %s\n", settingsPath)
	}
	return 0
}

func doctorHere(stdout, stderr io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	return runDoctor(dir, stdout)
}

func runDoctor(dir string, stdout io.Writer) int {
	results := doctor.Run(setup.SettingsPath(dir), version, doctorTimeout)
	doctor.Print(stdout, results)
	if !doctor.AllOK(results) {
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
