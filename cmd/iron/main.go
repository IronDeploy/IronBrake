package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/cloudtrail"
	"github.com/IronDeploy/IronBrake/internal/dialog"
	"github.com/IronDeploy/IronBrake/internal/doctor"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/policy"
	"github.com/IronDeploy/IronBrake/internal/rules"
	"github.com/IronDeploy/IronBrake/internal/runenv"
	"github.com/IronDeploy/IronBrake/internal/safefile"
	"github.com/IronDeploy/IronBrake/internal/scan"
	"github.com/IronDeploy/IronBrake/internal/session"
	"github.com/IronDeploy/IronBrake/internal/setup"
	"github.com/IronDeploy/IronBrake/internal/shieldui"
	"github.com/IronDeploy/IronBrake/internal/tfplan"
	"github.com/IronDeploy/IronBrake/internal/toolpath"
	"github.com/IronDeploy/IronBrake/internal/watch"
)

// version é definida na compilação com -ldflags "-X main.version=...".
var version = "dev"

const (
	approvedByUserReason = "Iron Brake: aprovado pelo usuário na janela de confirmação."
	rejectedByUserReason = "Iron Brake: o usuário recusou este comando na janela de confirmação. não tente de novo; siga com o resto da tarefa ou pergunte ao usuário o que fazer."
	timeoutReason        = "Iron Brake: a análise passou do tempo limite; bloqueado por segurança. tente de novo ou peça ao usuário para executar."
	cannotAskReason      = "Iron Brake: este comando precisa da confirmação do usuário, mas este agente não sabe perguntar e a janela de confirmação não está disponível. não tente de novo; peça ao usuário para executá-lo."
)

const doctorTimeout = 5 * time.Second

const usage = `uso: iron <subcomando>

  hook          roda como hook de pré-execução do agente (lê o evento pelo stdin; --agent=claude)
  init          instala o hook em .claude/settings.json da pasta atual e a etiqueta do agente na AWS
  init --no-aws-tag  instala só o hook, sem gravar AWS_SDK_UA_APP_ID no env do agente
  init --harden grava também regras deny de leitura das credenciais (Iron Shield)
  doctor        verifica se o hook desta pasta está mesmo protegendo
  scan          raio-X das credenciais ao alcance do agente (Iron Shield, só leitura)
  scan --manage igual, mas interativo: setas navegam, enter oculta/mostra, esc/q sai
  shield status mostra quais credenciais estão travadas e quais não estão
  shield lock   trava a leitura das credenciais (não precisa de "init" antes)
  shield unlock destrava a leitura — credenciais ficam visíveis ao agente até travar de novo
  audit verify  confere se a corrente do log de auditoria (~/.iron/audit.log) está íntegra
  aws-alerts    imprime o modelo CloudFormation que alerta quando um agente chama API destrutiva na AWS
  aws-creds     portão de credenciais da AWS para o credential_process (iron aws-creds --help)
  watch         confere de fora se o hook está vendo o que o agente executa (iron watch --help)
  version       mostra a versão`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}

	switch args[0] {
	case "hook":
		agent, err := parseAgentFlag(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "iron: %v\n", err)
			return 2
		}
		budget := agent.Capabilities().Budget()
		return runHook(stdin, stdout, stderr, hookDeps{
			readPlan: showPlan, loadEnv: loadEnv, session: checkSession, audit: writeAudit,
			confirm:  func(reason string) dialog.Answer { return dialog.ConfirmWithin(reason, budget.DialogWait) },
			deadline: budget.Deadline, agent: agent,
		})
	case "init":
		return initHere(args[1:], stdout, stderr)
	case "doctor":
		return doctorHere(stdout, stderr)
	case "scan":
		if slices.Contains(args[1:], "--manage") {
			return runScanManage(stdout, stderr)
		}
		return runScan(stdout, stderr)
	case "shield":
		return runShield(args[1:], stdout, stderr)
	case "audit":
		return runAudit(args[1:], stdout, stderr)
	case "aws-alerts":
		fmt.Fprint(stdout, cloudtrail.Template())
		return 0
	case "aws-creds":
		if slices.Contains(args[1:], "--help") || slices.Contains(args[1:], "-h") {
			fmt.Fprintln(stdout, awsCredsUsage)
			return 0
		}
		return runAWSCredsCommand(args[1:], stdout, stderr)
	case "watch":
		if slices.Contains(args[1:], "--help") || slices.Contains(args[1:], "-h") {
			fmt.Fprintln(stdout, watchUsage)
			return 0
		}
		return runWatchCommand(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "iron %s\n", version)
		return 0
	default:
		fmt.Fprintf(stderr, "iron: subcomando desconhecido %q\n\n%s\n", args[0], usage)
		return 2
	}
}

// parseAgentFlag lê "--agent=NOME" (ou "--agent NOME"); sem a opção, vale o
// agente padrão.
func parseAgentFlag(args []string) (hook.Agent, error) {
	name := hook.DefaultAgent
	for i := 0; i < len(args); i++ {
		switch value, ok := strings.CutPrefix(args[i], "--agent="); {
		case ok:
			name = value
		case args[i] == "--agent" && i+1 < len(args):
			i++
			name = args[i]
		default:
			return nil, fmt.Errorf("opção desconhecida %q em \"iron hook\"", args[i])
		}
	}
	return hook.Lookup(name)
}

// hookDeps reúne o que o hook usa do mundo externo, para os testes trocarem.
type hookDeps struct {
	readPlan rules.PlanReader
	confirm  func(string) dialog.Answer
	loadEnv  func(cwd string) rules.Env
	session  func(sessionID string, a session.Activity) (hook.Decision, string)
	audit    func(audit.Entry, policy.AuditLog) error // a rotação vem do policy.yaml do projeto
	deadline time.Duration
	agent    hook.Agent // nil = hook.DefaultAgent
}

func (d hookDeps) agentOrDefault() hook.Agent {
	if d.agent != nil {
		return d.agent
	}
	agent, _ := hook.Lookup(hook.DefaultAgent)
	return agent
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
	agent := deps.agentOrDefault()

	go func() {
		event, err := agent.ParseEvent(stdin)
		if err != nil {
			v := verdict{decision: hook.Deny, reason: "iron: não consegui ler o evento do stdin", rule: "event"}
			done <- outcome{v, record(deps, audit.Entry{Agent: agent.Name(), Class: "evento ilegível"}, v, policy.AuditLog{})}
			return
		}
		v, changes, auditCfg := decide(event, deps)
		entry := audit.Entry{Session: event.SessionID, Agent: agent.Name(), Class: classOf(event), Resources: changes}
		done <- outcome{v, record(deps, entry, v, auditCfg)}
	}()

	select {
	case o := <-done:
		code := agent.Respond(stdout, stderr, o.v.decision, o.v.reason)
		if o.auditErr != nil {
			fmt.Fprintln(stderr, "iron: não consegui gravar o log de auditoria")
		}
		return code
	case <-time.After(deps.deadline):
		return agent.Respond(stdout, stderr, hook.Deny, timeoutReason)
	}
}

func decide(event hook.Event, deps hookDeps) (verdict, []audit.Resource, policy.AuditLog) {
	command := event.Command
	env := deps.loadEnv(event.Cwd)

	checked := rules.Evaluate(command, env)
	v := verdict{decision: checked.Decision, reason: checked.Reason, rule: checked.Rule}
	if v.decision == hook.Deny {
		return v, nil, env.Policy.AuditLog
	}

	apply := rules.InspectTerraformApply(command, event.Cwd, env, deps.readPlan)
	changes := auditResources(apply.Changes)
	v = combine(v, verdict{decision: apply.Decision, reason: apply.Reason, rule: ruleName(apply.Decision, rules.TerraformApplyRule)})
	if v.decision == hook.Deny {
		return v, changes, env.Policy.AuditLog
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
		v = confirmAsk(command, v, deps.confirm, deps.agentOrDefault().Capabilities().CanAsk)
	}
	return v, changes, env.Policy.AuditLog
}

// record grava a decisão no log. Uma falha não muda a decisão.
func record(deps hookDeps, entry audit.Entry, v verdict, cfg policy.AuditLog) error {
	entry.Decision = string(v.decision)
	entry.Rule = v.rule
	entry.Dialog = v.dialog
	return deps.audit(entry, cfg)
}

func classOf(event hook.Event) string {
	if !event.Shell {
		return "ferramenta " + event.Tool
	}
	if class := rules.Classify(event.Command); class != "" {
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

// showPlan lê o plano com o programa que o ~/.iron/config.yaml indica (ou o do
// PATH, se não for suspeito).
func showPlan(req tfplan.Request) ([]byte, error) {
	cfg, err := toolpath.Load(toolpath.ConfigPath())
	if err != nil {
		return nil, err
	}
	req.Config = cfg
	req.ProjectDirs = []string{hook.ProjectDir(os.Getenv), req.Cwd}
	return tfplan.Show(req)
}

func writeAudit(e audit.Entry, cfg policy.AuditLog) error {
	e.Time = time.Now()
	return auditLog(cfg).Append(e)
}

// auditLog aplica a rotação do policy.yaml (zero = padrão do audit).
func auditLog(cfg policy.AuditLog) audit.Log {
	return audit.Log{Path: audit.DefaultPath(), MaxSize: int64(cfg.MaxSizeMB) << 20, Keep: cfg.Keep}
}

// projectAuditConfig lê a rotação do policy.yaml do projeto atual, para os
// comandos que rodam no terminal (shield). Política ausente ou com erro: padrão.
func projectAuditConfig() policy.AuditLog {
	dir := hook.ProjectDir(os.Getenv)
	if dir == "" {
		dir, _ = os.Getwd()
	}
	p, _ := policy.Load(dir)
	return p.AuditLog
}

// loadEnv usa a política da raiz do projeto do agente (ou da pasta do comando).
// Política com erro vira PolicyError, e as regras tratam tudo como produção.
func loadEnv(cwd string) rules.Env {
	projectDir := hook.ProjectDir(os.Getenv)
	if projectDir == "" {
		projectDir = cwd
	}

	p, err := policy.Load(projectDir)
	return rules.Env{
		Policy:      p,
		Context:     runenv.Collect(cwd, os.Getenv, readContextFile),
		PolicyError: err != nil,
		Cwd:         cwd,
		DataDir:     os.Getenv("TF_DATA_DIR"),
		ReadFile:    readContextFile,
	}
}

func readContextFile(path string) ([]byte, error) {
	return safefile.Read(path, 4<<20)
}

// confirmAsk mostra o motivo numa janela do sistema, porque a caixa do Claude
// Code não o mostra antes da decisão. Se a janela não resolveu e o agente não
// entende "ask", o resultado é deny: nunca allow.
func confirmAsk(command string, v verdict, confirm func(string) dialog.Answer, canAsk bool) verdict {
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

	if v.decision == hook.Ask && !canAsk {
		v.decision, v.reason = hook.Deny, cannotAskReason+"\n\n"+v.reason
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
		fmt.Fprintf(stdout, "iron: corrente íntegra: %d linha(s) conferida(s) em %s (%d arquivo(s)).\n", r.Lines, log.Path, r.Files)
		fmt.Fprintln(stdout, "      (a corrente não detecta se o arquivo inteiro ou as últimas linhas foram apagados.)")
		return 0
	case r.BrokenAt > 0:
		fmt.Fprintf(stdout, "iron: corrente QUEBRADA na linha %d (arquivo %s): %s.\n", r.BrokenAt, r.File, r.Reason)
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

func initHere(args []string, stdout, stderr io.Writer) int {
	opts := initOptions{harden: slices.Contains(args, "--harden"), awsTag: !slices.Contains(args, "--no-aws-tag")}
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
	// os.Executable() não normaliza: "../bin/iron" vira ".../a/../bin/iron", e o
	// hook ficaria gravado com esse caminho.
	return runInit(dir, filepath.Clean(exePath), opts, stdout, stderr)
}

type initOptions struct {
	harden bool // grava as regras deny de leitura de credenciais (Iron Shield)
	awsTag bool // grava AWS_SDK_UA_APP_ID no env do agente, para o CloudTrail
}

// awsTagVar é a variável do SDK da AWS que acrescenta app/<valor> ao user agent.
const awsTagVar = "AWS_SDK_UA_APP_ID"

func runInit(dir, exePath string, opts initOptions, stdout, stderr io.Writer) int {
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

	if opts.awsTag {
		reportAWSTag(settingsPath, stdout, stderr)
	}

	if opts.harden {
		added, err := setup.Harden(settingsPath)
		if err != nil {
			fmt.Fprintf(stderr, "iron: %v\n", err)
			return 1
		}
		if len(added) > 0 {
			fmt.Fprintf(stdout, "iron: %d regra(s) deny de leitura de credenciais gravada(s) (Iron Shield)\n", len(added))
		} else {
			fmt.Fprintln(stdout, "iron: as regras deny de credenciais já estavam no lugar")
		}
	}
	return 0
}

// reportAWSTag grava a etiqueta do agente e diz o que fez. Falhar aqui não
// desfaz a instalação do hook: é um aviso.
func reportAWSTag(settingsPath string, stdout, stderr io.Writer) {
	appID := hook.Claude.Capabilities().AppID
	changed, current, err := setup.SetEnv(settingsPath, awsTagVar, appID)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "iron: aviso: não consegui gravar %s: %v\n", awsTagVar, err)
	case changed:
		fmt.Fprintf(stdout, "iron: etiqueta do agente na AWS gravada (%s=%s): o CloudTrail passa a mostrar app/%s nas chamadas dele\n", awsTagVar, appID, appID)
	case current == appID:
		fmt.Fprintf(stdout, "iron: a etiqueta do agente na AWS já estava gravada (%s=%s)\n", awsTagVar, appID)
	default:
		fmt.Fprintf(stdout, "iron: aviso: %s já vale %q neste projeto; não alterei, e o CloudTrail não vai mostrar app/%s\n", awsTagVar, current, appID)
	}
}

// localFilesystem monta o Filesystem que o Iron Shield varre nesta máquina:
// leitura sem travar (safefile) e sem seguir link nenhum além do que
// os.ReadDir já resolve. "iron scan" e "iron scan --manage" usam exatamente
// a mesma varredura — o manage não reimplementa nada, só mostra o resultado
// de um jeito interativo.
func localFilesystem() (scan.Filesystem, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return scan.Filesystem{}, fmt.Errorf("não consegui achar o home: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return scan.Filesystem{}, err
	}

	return scan.Filesystem{
		Home: home,
		Cwd:  cwd,
		Read: func(path string) ([]byte, error) { return safefile.Read(path, 1<<20) },
		List: func(dir string) ([]string, error) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			return names, nil
		},
	}, nil
}

// runScan roda o Iron Shield: raio-X de credenciais, só leitura e sem rede.
func runScan(stdout, stderr io.Writer) int {
	fs, err := localFilesystem()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}

	findings := scan.Scan(fs, envMap(os.Environ()))
	fmt.Fprint(stdout, scan.Report(findings))
	return 0
}

// runScanManage roda o mesmo raio-X do "iron scan", mas numa tela interativa:
// setas navegam entre as categorias achadas, enter oculta/mostra cada uma
// (grava/apaga as regras deny na hora) e esc/q sai. Precisa de um terminal de
// verdade — por isso usa os.Stdin/os.Stdout direto, não os io.Reader/Writer
// injetados no resto do "iron" (raw mode opera no descritor do arquivo).
func runScanManage(stdout, stderr io.Writer) int {
	fs, err := localFilesystem()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	settingsPath := setup.SettingsPath(dir)

	findings := scan.Scan(fs, envMap(os.Environ()))
	onToggle := func(action, category string, n int) {
		logShieldEventFor(stderr, action, fmt.Sprintf("%s: %d regra(s)", category, n))
	}

	if err := shieldui.Run(settingsPath, findings, os.Stdin, os.Stdout, onToggle); err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	return 0
}

// runShield trava/destrava/mostra o estado do bloqueio de leitura das
// credenciais (permissions.deny). Funciona mesmo sem "iron init" ter rodado
// antes: lock cria o settings do zero, igual a "init --harden".
func runShield(args []string, stdout, stderr io.Writer) int {
	const shieldUsage = "uso: iron shield status|lock|unlock"
	if len(args) != 1 {
		fmt.Fprintln(stderr, shieldUsage)
		return 2
	}

	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	settingsPath := setup.SettingsPath(dir)

	switch args[0] {
	case "status":
		return shieldStatus(settingsPath, stdout, stderr)
	case "lock":
		return shieldLock(settingsPath, stdout, stderr)
	case "unlock":
		return shieldUnlock(settingsPath, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "iron: subcomando de shield desconhecido %q\n\n%s\n", args[0], shieldUsage)
		return 2
	}
}

func shieldStatus(settingsPath string, stdout, stderr io.Writer) int {
	st, err := setup.Status(settingsPath)
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	total := len(st.Locked) + len(st.Unlocked)
	if len(st.Unlocked) == 0 {
		fmt.Fprintf(stdout, "iron: Iron Shield TRAVADO — %d/%d regra(s) bloqueando leitura de credenciais em %s\n", len(st.Locked), total, settingsPath)
		return 0
	}
	fmt.Fprintf(stdout, "iron: Iron Shield DESTRAVADO — %d/%d regra(s) faltando em %s\n", len(st.Unlocked), total, settingsPath)
	for _, rule := range st.Unlocked {
		fmt.Fprintf(stdout, "   • %s\n", rule)
	}
	fmt.Fprintln(stdout, "rode \"iron shield lock\" para travar.")
	return 0
}

func shieldLock(settingsPath string, stdout, stderr io.Writer) int {
	added, err := setup.Harden(settingsPath)
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	logShieldEvent(stderr, "lock", len(added))
	if len(added) > 0 {
		fmt.Fprintf(stdout, "iron: %d regra(s) deny gravada(s). Iron Shield travado.\n", len(added))
	} else {
		fmt.Fprintln(stdout, "iron: Iron Shield já estava travado.")
	}
	return 0
}

func shieldUnlock(settingsPath string, stdout, stderr io.Writer) int {
	removed, err := setup.Unharden(settingsPath)
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	logShieldEvent(stderr, "unlock", len(removed))
	if len(removed) > 0 {
		fmt.Fprintf(stdout, "iron: %d regra(s) deny removida(s). Iron Shield DESTRAVADO — as credenciais voltam a ficar visíveis ao agente.\n", len(removed))
		fmt.Fprintln(stdout, "      rode \"iron shield lock\" assim que terminar o que precisa fazer.")
	} else {
		fmt.Fprintln(stdout, "iron: Iron Shield já estava destravado (nenhuma regra para remover).")
	}
	return 0
}

// logShieldEvent grava lock/unlock no log de auditoria: destravar credenciais
// é uma decisão de risco e precisa ficar no mesmo rastro que as do hook.
func logShieldEvent(stderr io.Writer, action string, count int) {
	logShieldEventFor(stderr, action, fmt.Sprintf("%d regra(s)", count))
}

// logShieldEventFor é o logShieldEvent genérico: rule é o texto livre que
// identifica o que mudou ("N regra(s)" para lock/unlock de tudo, ou
// "<categoria>: N regra(s)" para um toggle individual no scan --manage).
func logShieldEventFor(stderr io.Writer, action, rule string) {
	entry := audit.Entry{Class: "iron shield", Decision: action, Rule: rule}
	if err := writeAudit(entry, projectAuditConfig()); err != nil {
		fmt.Fprintln(stderr, "iron: não consegui gravar o log de auditoria")
	}
}

// envMap transforma os.Environ() ("NOME=valor") em mapa nome→valor.
func envMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok {
			m[name] = value
		}
	}
	return m
}

func doctorHere(stdout, stderr io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	}
	return runDoctor(dir, stdout)
}

// coverageWindow é o quanto o doctor olha para trás ao conferir se o hook está
// vendo os comandos; nunca antes de o hook ser instalado.
const coverageWindow = 24 * time.Hour

func runDoctor(dir string, stdout io.Writer) int {
	log := audit.Log{Path: audit.DefaultPath()}
	settingsPath := setup.SettingsPath(dir)
	extra := doctor.Extra{
		Log:        log,
		ConfigPath: toolpath.ConfigPath(),
		ProjectDir: dir,
		AWSTag:     hook.Claude.Capabilities().AppID,
	}
	if home, err := os.UserHomeDir(); err == nil {
		since := time.Now().Add(-coverageWindow)
		if info, err := os.Stat(settingsPath); err == nil && info.ModTime().After(since) {
			since = info.ModTime() // o hook não existia antes disto
		}
		extra.Coverage = &doctor.Coverage{
			Dirs:   []string{watch.TranscriptDir(home, dir)},
			Source: watch.Source{Classify: rules.Classify, Entries: log.Entries},
			Since:  since,
		}
	}

	results := doctor.RunAll(settingsPath, version, doctorTimeout, extra)
	doctor.Print(stdout, results)
	if !doctor.AllOK(results) {
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
