package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/awsgate"
	"github.com/IronDeploy/IronBrake/internal/dialog"
)

const awsCredsUsage = `uso: iron aws-creds [opções] -- COMANDO [ARGS...]

Portão de credenciais da AWS, para o "credential_process" do ~/.aws/config.
COMANDO é quem entrega as credenciais de verdade (JSON, Version 1), por exemplo:

  [profile prod]
  credential_process = /usr/local/bin/iron aws-creds --env production -- /usr/local/bin/aws configure export-credentials --profile prod-real --format process

Humano no terminal passa direto. Agente (Claude, Kiro, Gemini, Codex, Cursor entre
os processos ancestrais) em conta que não é produção passa e fica no log. Agente
em produção só recebe depois de você clicar em "Executar" na janela.

  --env MODO        auto (padrão: o nome do perfil diz, ex.: prod), production,
                    strict (produção, salvo nome de teste) ou dev
  --label NOME      nome do perfil na janela e no log. Escreva sempre: o SDK não passa o
                    perfil escolhido com --profile, e sem --label no modo auto o pedido
                    é tratado como PRODUÇÃO (não há como saber qual conta é)
  --approve-for DUR quanto tempo vale a liberação (padrão 15m)
  --wait DUR        quanto a janela espera (padrão 45s; o SDK desiste em ~1 min)
  --require-tty     sem agente nos ancestrais e sem terminal de controle (processo
                    desligado do pai, cron, CI), trata como agente. Recomendado em
                    perfil de produção de uso humano; não use em perfil de CI`

const (
	defaultApproveFor = 15 * time.Minute
	defaultWait       = 45 * time.Second
	credentialTimeout = 60 * time.Second
	maxCredentialSize = 64 << 10
)

type awsCredsOptions struct {
	labelGiven   bool // --label foi escrito na linha do credential_process
	labelUnknown bool // modo auto sem --label: tratado como produção
	requireTTY   bool
	env          string
	label        string
	approveFor   time.Duration
	wait         time.Duration
	command      []string
}

func parseAWSCredsArgs(args []string, getenv func(string) string) (awsCredsOptions, error) {
	opts := awsCredsOptions{env: awsgate.EnvAuto, approveFor: defaultApproveFor, wait: defaultWait}

	split := -1
	for i, a := range args {
		if a == "--" {
			split = i
			break
		}
	}
	if split < 0 || split == len(args)-1 {
		return opts, errors.New(`falta o comando que entrega as credenciais, depois de "--"`)
	}
	opts.command = args[split+1:]
	flags := args[:split]

	for i := 0; i < len(flags); i++ {
		name, value, hasValue := strings.Cut(flags[i], "=")
		if name == "--require-tty" && !hasValue {
			opts.requireTTY = true
			continue
		}
		next := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(flags) {
				return "", fmt.Errorf("%s precisa de um valor", name)
			}
			i++
			return flags[i], nil
		}

		v, err := next()
		if err != nil {
			return opts, fmt.Errorf("opção desconhecida ou sem valor: %q", flags[i])
		}
		switch name {
		case "--env":
			if !awsgate.ValidEnv(v) {
				return opts, fmt.Errorf("--env vai de auto, production, strict ou dev; recebi %q", v)
			}
			opts.env = v
		case "--label":
			opts.label, opts.labelGiven = v, true
		case "--approve-for", "--wait":
			d, perr := time.ParseDuration(v)
			if perr != nil || d <= 0 {
				return opts, fmt.Errorf("valor inválido em %s: %q", name, v)
			}
			if name == "--approve-for" {
				opts.approveFor = d
			} else {
				opts.wait = d
			}
		default:
			return opts, fmt.Errorf("opção desconhecida %q", flags[i])
		}
	}

	if opts.label == "" {
		opts.label = getenv("AWS_PROFILE")
	}
	if opts.label == "" {
		opts.label = "aws"
	}
	opts.label = awsgate.SanitizeLabel(opts.label)
	return opts, nil
}

// awsCredsDeps reúne o que o portão usa do mundo externo, para os testes trocarem.
type awsCredsDeps struct {
	now       func() time.Time
	getenv    func(string) string
	requester func() (requester, error)
	approvals awsgate.Approvals
	confirm   func(message string, wait time.Duration) dialog.Answer
	run       func(ctx context.Context, argv []string, stderr io.Writer) ([]byte, error)
	record    func(audit.Entry) error
}

func runAWSCredsCommand(args []string, stdout, stderr io.Writer) int {
	return runAWSCreds(args, stdout, stderr, awsCredsDeps{
		now:       time.Now,
		getenv:    os.Getenv,
		requester: detectRequester,
		approvals: awsgate.Approvals{Dir: awsgate.DefaultApprovalsDir()},
		confirm:   dialog.ConfirmWithin,
		run:       runCredentialCommand,
		record:    func(e audit.Entry) error { return writeAudit(e, projectAuditConfig()) },
	})
}

// requester é quem pediu as credenciais: o agente (vazio = humano) e se algum
// processo da cadeia tem terminal de controle.
type requester struct {
	agent    string
	terminal bool
}

func detectRequester() (requester, error) {
	procs, err := awsgate.ListProcesses()
	if err != nil {
		return requester{}, err
	}
	chain := awsgate.Chain(procs, os.Getppid())
	return requester{agent: awsgate.DetectAgent(chain), terminal: awsgate.HasTerminal(chain)}, nil
}

func runCredentialCommand(ctx context.Context, argv []string, stderr io.Writer) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stderr = stderr // pedidos de login do SSO e erros do comando real
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if len(out) > maxCredentialSize {
		return nil, errors.New("a saída é grande demais para ser uma credencial")
	}
	return out, nil
}

func runAWSCreds(args []string, stdout, stderr io.Writer, deps awsCredsDeps) int {
	opts, err := parseAWSCredsArgs(args, deps.getenv)
	if err != nil {
		fmt.Fprintf(stderr, "iron: %v\n\n%s\n", err, awsCredsUsage)
		return 2
	}

	who, err := deps.requester()
	agent := who.agent
	switch {
	case errors.Is(err, awsgate.ErrUnsupported):
		fmt.Fprintf(stderr, "iron: %v\n", err)
		return 1
	case err != nil:
		// Sem saber quem pediu, trata como agente: na dúvida, não entrega de graça.
		fmt.Fprintf(stderr, "iron: aviso: não consegui identificar quem pediu as credenciais (%v)\n", err)
		agent = "desconhecido"
	case agent == "" && opts.requireTTY && !who.terminal:
		agent = "sem-terminal"
	}

	production := awsgate.IsProduction(opts.env, opts.label, deps.getenv("AWS_PROFILE"))
	// Sem --label o portão só tem o AWS_PROFILE, que o agente controla (e que o
	// "--profile" nem preenche): não dá para saber qual conta é, então vale como
	// produção. Os modos que não leem o nome (production, dev, strict) não mudam.
	if opts.env == awsgate.EnvAuto && !opts.labelGiven {
		production, opts.labelUnknown = true, true
	}
	now := deps.now()
	action := awsgate.Decide(agent, production, deps.approvals.Valid(agent, opts.label, now))

	entry := audit.Entry{
		Agent:     agent,
		Class:     "aws credentials",
		Rule:      "aws-gate",
		Resources: []audit.Resource{{Address: opts.label, Action: "credentials"}},
	}

	switch action {
	case awsgate.Pass:
		return deliver(opts, stdout, stderr, deps)
	case awsgate.Allow:
		entry.Decision = "allow"
	case awsgate.Window:
		entry.Decision, entry.Dialog = "allow", "window"
	case awsgate.Prompt:
		release, err := deps.approvals.PromptLock(opts.wait + 5*time.Second)
		if err != nil {
			// Outra janela ainda está aberta (ou a trava falhou): não empilha uma
			// segunda pergunta nem libera.
			entry.Decision, entry.Dialog = "deny", string(dialog.Unavailable)
			recordGate(deps, entry, stderr)
			fmt.Fprintln(stderr, deniedCredentialsReason(agent, opts.label, dialog.Unavailable))
			return 1
		}
		defer release()

		// Quem esperava a trava confere de novo: a janela que acabou de fechar pode ter liberado.
		if deps.approvals.Valid(agent, opts.label, deps.now()) {
			entry.Decision, entry.Dialog = "allow", "window"
			break
		}

		answer := deps.confirm(credentialsMessage(agent, opts), opts.wait)
		entry.Dialog = string(answer)
		if answer != dialog.Approved {
			entry.Decision = "deny"
			recordGate(deps, entry, stderr)
			fmt.Fprintln(stderr, deniedCredentialsReason(agent, opts.label, answer))
			return 1
		}
		entry.Decision = string(approvedByUser)
		granted := deps.now()
		if err := deps.approvals.Grant(agent, opts.label, granted, granted.Add(opts.approveFor)); err != nil {
			fmt.Fprintf(stderr, "iron: aviso: não consegui gravar a liberação (%v); vale só para este pedido\n", err)
		}
	}

	recordGate(deps, entry, stderr)
	return deliver(opts, stdout, stderr, deps)
}

// approvedByUser é o mesmo valor que o hook grava quando o usuário aprova na janela.
const approvedByUser = "userApproved"

func recordGate(deps awsCredsDeps, entry audit.Entry, stderr io.Writer) {
	if err := deps.record(entry); err != nil {
		fmt.Fprintln(stderr, "iron: não consegui gravar o log de auditoria")
	}
}

// deliver roda o comando real e repassa a saída sem mexer, depois de conferir
// que é uma credencial no formato do credential_process.
func deliver(opts awsCredsOptions, stdout, stderr io.Writer, deps awsCredsDeps) int {
	ctx, cancel := context.WithTimeout(context.Background(), credentialTimeout)
	defer cancel()

	out, err := deps.run(ctx, opts.command, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "iron: o comando que entrega as credenciais falhou: %v\n", err)
		return 1
	}

	var creds struct {
		Version         int    `json:"Version"`
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &creds) != nil || creds.Version != 1 || creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		fmt.Fprintln(stderr, "iron: a saída do comando não é uma credencial válida (esperava o JSON do credential_process, Version 1)")
		return 1
	}

	stdout.Write(out)
	return 0
}

func credentialsMessage(agent string, opts awsCredsOptions) string {
	why := "que é PRODUÇÃO"
	if opts.labelUnknown {
		why = "tratado como PRODUÇÃO (a linha do credential_process não tem --label)"
	}
	return fmt.Sprintf("IRON BRAKE — credenciais da AWS\n\n"+
		"O agente %s pediu as credenciais do perfil %q, %s.\n\n"+
		"Executar libera esse agente neste perfil por %s. Cancelar nega.",
		agent, opts.label, why, humanDuration(opts.approveFor))
}

func deniedCredentialsReason(agent, label string, answer dialog.Answer) string {
	why := "o usuário recusou na janela de confirmação"
	if answer == dialog.Unavailable {
		why = "a janela de confirmação não está disponível"
	}
	return fmt.Sprintf("Iron Brake: credenciais da AWS de produção (perfil %s) não liberadas para o agente %s: %s. "+
		"não tente de novo nem outro caminho; peça ao usuário para executar.", label, agent, why)
}

func humanDuration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d s", int(d.Seconds()))
}
