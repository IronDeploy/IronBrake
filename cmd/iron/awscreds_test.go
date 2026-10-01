package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/awsgate"
	"github.com/IronDeploy/IronBrake/internal/dialog"
)

const goodCreds = `{"Version":1,"AccessKeyId":"AKIAEXEMPLO","SecretAccessKey":"segredo-de-teste","SessionToken":"tok","Expiration":"2026-10-01T15:00:00Z"}`

type gateFixture struct {
	deps      awsCredsDeps
	entries   []audit.Entry
	messages  []string
	waits     []time.Duration
	answer    dialog.Answer
	ran       int
	runOut    string
	runErr    error
	agent     string
	noTTY     bool
	reqErr    error
	env       map[string]string
	now       time.Time
	approvals awsgate.Approvals
}

func newGate(t *testing.T) *gateFixture {
	t.Helper()
	f := &gateFixture{
		answer: dialog.Approved,
		runOut: goodCreds,
		env:    map[string]string{},
		now:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	f.approvals = awsgate.Approvals{Dir: filepath.Join(t.TempDir(), "gate")}
	f.deps = awsCredsDeps{
		now:       func() time.Time { return f.now },
		getenv:    func(k string) string { return f.env[k] },
		requester: func() (requester, error) { return requester{agent: f.agent, terminal: !f.noTTY}, f.reqErr },
		approvals: f.approvals,
		confirm: func(message string, wait time.Duration) dialog.Answer {
			f.messages = append(f.messages, message)
			f.waits = append(f.waits, wait)
			return f.answer
		},
		run: func(ctx context.Context, argv []string, stderr io.Writer) ([]byte, error) {
			f.ran++
			return []byte(f.runOut), f.runErr
		},
		record: func(e audit.Entry) error { f.entries = append(f.entries, e); return nil },
	}
	return f
}

func (f *gateFixture) run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = runAWSCreds(args, &out, &errb, f.deps)
	return code, out.String(), errb.String()
}

func TestParseAWSCredsArgs(t *testing.T) {
	env := func(k string) string {
		if k == "AWS_PROFILE" {
			return "do-ambiente"
		}
		return ""
	}

	got, err := parseAWSCredsArgs([]string{"--", "aws", "x"}, env)
	if err != nil || got.env != "auto" || got.label != "do-ambiente" || got.approveFor != 15*time.Minute || got.wait != 45*time.Second ||
		strings.Join(got.command, " ") != "aws x" {
		t.Errorf("padrões: %+v %v", got, err)
	}

	if got, err := parseAWSCredsArgs([]string{"--require-tty", "--", "x"}, env); err != nil || !got.requireTTY {
		t.Errorf("--require-tty: %+v %v", got, err)
	}

	got, err = parseAWSCredsArgs([]string{"--env", "production", "--label=meu perfil", "--approve-for", "1h", "--wait=30s", "--", "cmd", "--env", "dev"}, env)
	if err != nil || got.env != "production" || got.label != "meu?perfil" || got.approveFor != time.Hour || got.wait != 30*time.Second ||
		strings.Join(got.command, " ") != "cmd --env dev" {
		t.Errorf("opções: %+v %v", got, err)
	}

	if got, _ := parseAWSCredsArgs([]string{"--", "x"}, func(string) string { return "" }); got.label != "aws" {
		t.Errorf("sem perfil nenhum o nome é aws: %q", got.label)
	}

	for _, bad := range [][]string{
		nil, {"--env", "production"}, {"--"}, {"--env", "talvez", "--", "x"}, {"--wait", "abc", "--", "x"},
		{"--wait", "-1s", "--", "x"}, {"--bogus", "1", "--", "x"}, {"--env", "--", "x"}, {"--label"},
	} {
		if _, err := parseAWSCredsArgs(bad, env); err == nil {
			t.Errorf("%v deveria dar erro", bad)
		}
	}
}

func TestAWSCredsHumanPassesWithoutRecord(t *testing.T) {
	f := newGate(t)
	f.agent = ""

	code, stdout, stderr := f.run("--env", "production", "--", "real")

	if code != 0 || stdout != goodCreds || stderr != "" {
		t.Errorf("humano: %d %q %q", code, stdout, stderr)
	}
	if len(f.entries) != 0 || len(f.messages) != 0 {
		t.Errorf("humano não gera log nem janela: %+v %v", f.entries, f.messages)
	}
}

func TestAWSCredsAgentOutsideProductionIsRecorded(t *testing.T) {
	f := newGate(t)
	f.agent = "claude"

	code, stdout, _ := f.run("--label", "staging", "--", "real")

	if code != 0 || stdout != goodCreds || len(f.messages) != 0 {
		t.Fatalf("%d %q janelas=%v", code, stdout, f.messages)
	}
	if len(f.entries) != 1 {
		t.Fatalf("esperava 1 entrada: %+v", f.entries)
	}
	e := f.entries[0]
	if e.Agent != "claude" || e.Class != "aws credentials" || e.Rule != "aws-gate" || e.Decision != "allow" || e.Dialog != "" ||
		len(e.Resources) != 1 || e.Resources[0].Address != "staging" || e.Resources[0].Action != "credentials" {
		t.Errorf("entrada: %+v", e)
	}
}

func TestAWSCredsProductionApprovalWindow(t *testing.T) {
	f := newGate(t)
	f.agent = "claude"

	// 1º pedido: pergunta; aprovado.
	code, stdout, _ := f.run("--label", "prod", "--", "real")
	if code != 0 || stdout != goodCreds || len(f.messages) != 1 {
		t.Fatalf("1º pedido: %d %q janelas=%d", code, stdout, len(f.messages))
	}
	msg := f.messages[0]
	for _, want := range []string{"claude", `"prod"`, "PRODUÇÃO", "15 min"} {
		if !strings.Contains(msg, want) {
			t.Errorf("a janela deveria dizer %q:\n%s", want, msg)
		}
	}
	if f.waits[0] != 45*time.Second {
		t.Errorf("espera da janela: %v", f.waits[0])
	}

	// 2º pedido logo depois: vale a liberação, sem janela.
	f.now = f.now.Add(5 * time.Minute)
	if code, stdout, _ = f.run("--label", "prod", "--", "real"); code != 0 || stdout != goodCreds || len(f.messages) != 1 {
		t.Fatalf("2º pedido: %d %q janelas=%d", code, stdout, len(f.messages))
	}

	// Outro perfil de produção e outro agente não aproveitam a liberação.
	f.run("--label", "prod-eu", "--", "real")
	f.agent = "kiro"
	f.run("--label", "prod", "--", "real")
	if len(f.messages) != 3 {
		t.Errorf("perfil e agente diferentes perguntam de novo: %d janelas", len(f.messages))
	}

	// Vencida: pergunta de novo.
	f.agent = "claude"
	f.now = f.now.Add(20 * time.Minute)
	f.run("--label", "prod", "--", "real")
	if len(f.messages) != 4 {
		t.Errorf("liberação vencida deve perguntar: %d janelas", len(f.messages))
	}

	want := []struct{ decision, dialog string }{
		{"userApproved", "approved"}, {"allow", "window"}, {"userApproved", "approved"}, {"userApproved", "approved"}, {"userApproved", "approved"},
	}
	if len(f.entries) != len(want) {
		t.Fatalf("entradas: %+v", f.entries)
	}
	for i, w := range want {
		if f.entries[i].Decision != w.decision || f.entries[i].Dialog != w.dialog {
			t.Errorf("entrada %d: %+v, esperava %v", i, f.entries[i], w)
		}
	}
}

func TestAWSCredsProductionDenied(t *testing.T) {
	cases := []struct {
		name   string
		answer dialog.Answer
		want   string
	}{
		{"recusado", dialog.Rejected, "recusou na janela"},
		{"janela indisponível", dialog.Unavailable, "não está disponível"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGate(t)
			f.agent, f.answer = "gemini", c.answer

			code, stdout, stderr := f.run("--env", "production", "--label", "conta", "--", "real")

			if code != 1 || stdout != "" || f.ran != 0 {
				t.Errorf("%d stdout=%q executou=%d: o comando real não pode rodar", code, stdout, f.ran)
			}
			if !strings.Contains(stderr, c.want) || !strings.Contains(stderr, "gemini") || !strings.Contains(stderr, "conta") {
				t.Errorf("stderr: %q", stderr)
			}
			if len(f.entries) != 1 || f.entries[0].Decision != "deny" || f.entries[0].Dialog != string(c.answer) {
				t.Errorf("entrada: %+v", f.entries)
			}
			// Recusa não cria liberação: o próximo pedido pergunta de novo.
			f.answer = dialog.Approved
			f.run("--env", "production", "--label", "conta", "--", "real")
			if len(f.messages) != 2 {
				t.Errorf("depois de uma recusa deve perguntar de novo: %d", len(f.messages))
			}
		})
	}
}

func TestAWSCredsWithoutLabelIsTreatedAsProduction(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		awsProfile string
		wantAsk    bool
	}{
		// O agente controla o AWS_PROFILE e o --profile nem o preenche: sem --label, produção.
		{"auto sem --label e sem AWS_PROFILE", []string{"--"}, "", true},
		{"auto sem --label, AWS_PROFILE de teste", []string{"--"}, "dev", true},
		{"auto com --label de teste", []string{"--label", "staging", "--"}, "", false},
		{"auto com --label, AWS_PROFILE de teste não afrouxa", []string{"--label", "prod", "--"}, "dev", true},
		{"dev explícito sem --label", []string{"--env", "dev", "--"}, "", false},
		{"production sem --label", []string{"--env", "production", "--"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGate(t)
			f.agent = "claude"
			f.env["AWS_PROFILE"] = c.awsProfile

			f.run(append(c.args, "real")...)

			if asked := len(f.messages) > 0; asked != c.wantAsk {
				t.Errorf("perguntou=%v, esperava %v", asked, c.wantAsk)
			}
		})
	}

	f := newGate(t)
	f.agent = "claude"
	f.run("--", "real")
	if len(f.messages) != 1 || !strings.Contains(f.messages[0], "não tem --label") {
		t.Errorf("a janela deve explicar por que é produção: %v", f.messages)
	}
}

func TestAWSCredsConcurrentRequestsAskOnce(t *testing.T) {
	shared := awsgate.Approvals{Dir: filepath.Join(t.TempDir(), "gate")}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	var mu sync.Mutex
	asked := 0
	var entries []audit.Entry
	deps := awsCredsDeps{
		now:       func() time.Time { return now },
		getenv:    func(string) string { return "" },
		requester: func() (requester, error) { return requester{agent: "claude", terminal: false}, nil },
		approvals: shared,
		confirm: func(string, time.Duration) dialog.Answer {
			mu.Lock()
			asked++
			mu.Unlock()
			time.Sleep(150 * time.Millisecond) // o usuário demora para clicar
			return dialog.Approved
		},
		run: func(context.Context, []string, io.Writer) ([]byte, error) { return []byte(goodCreds), nil },
		record: func(e audit.Entry) error {
			mu.Lock()
			entries = append(entries, e)
			mu.Unlock()
			return nil
		},
	}

	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out, errb bytes.Buffer
			codes[i] = runAWSCreds([]string{"--env", "production", "--label", "prod", "--", "real"}, &out, &errb, deps)
		}(i)
	}
	wg.Wait()

	for i, c := range codes {
		if c != 0 {
			t.Errorf("pedido %d saiu com %d", i, c)
		}
	}
	if asked != 1 {
		t.Errorf("5 pedidos simultâneos devem abrir 1 janela só, abriram %d", asked)
	}
	approved, window := 0, 0
	for _, e := range entries {
		switch e.Dialog {
		case "approved":
			approved++
		case "window":
			window++
		}
	}
	if approved != 1 || window != 4 {
		t.Errorf("esperava 1 aprovação e 4 usos da liberação: %+v", entries)
	}
}

func TestAWSCredsDeniesWhenAnotherWindowIsStillOpen(t *testing.T) {
	f := newGate(t)
	f.agent = "claude"
	// Outra instância segura a trava durante todo o prazo deste pedido.
	release, err := f.approvals.PromptLock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	code, stdout, stderr := f.run("--env", "production", "--label", "prod", "--wait", "10ms", "--", "real")

	if code != 1 || stdout != "" || f.ran != 0 || len(f.messages) != 0 {
		t.Errorf("%d %q executou=%d janelas=%d: não pode empilhar janela nem liberar", code, stdout, f.ran, len(f.messages))
	}
	if !strings.Contains(stderr, "não está disponível") {
		t.Errorf("stderr: %q", stderr)
	}
}

func TestAWSCredsUnknownRequesterIsTreatedAsAgent(t *testing.T) {
	f := newGate(t)
	f.reqErr = errors.New("ps falhou")
	f.agent = ""

	code, _, stderr := f.run("--env", "production", "--", "real")

	if code != 0 || len(f.messages) != 1 || !strings.Contains(f.messages[0], "desconhecido") {
		t.Errorf("sem saber quem pediu, trata como agente e pergunta: %d %v", code, f.messages)
	}
	if !strings.Contains(stderr, "não consegui identificar") {
		t.Errorf("deveria avisar: %q", stderr)
	}
	if len(f.entries) != 1 || f.entries[0].Agent != "desconhecido" {
		t.Errorf("entrada: %+v", f.entries)
	}
}

func TestAWSCredsRequireTTY(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		agent     string
		noTTY     bool
		wantAsk   bool
		wantAgent string
	}{
		{"humano com terminal passa", []string{"--require-tty", "--env", "production"}, "", false, false, ""},
		{"processo desligado vira agente", []string{"--require-tty", "--env", "production"}, "", true, true, "sem-terminal"},
		{"sem a opção, desligado passa como humano", []string{"--env", "production"}, "", true, false, ""},
		{"agente continua agente", []string{"--require-tty", "--env", "production"}, "claude", false, true, "claude"},
		{"fora de produção só registra", []string{"--require-tty", "--env", "dev"}, "", true, false, "sem-terminal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGate(t)
			f.agent, f.noTTY = c.agent, c.noTTY

			code, stdout, _ := f.run(append(c.args, "--", "real")...)

			if code != 0 || stdout != goodCreds {
				t.Fatalf("%d %q", code, stdout)
			}
			if asked := len(f.messages) > 0; asked != c.wantAsk {
				t.Errorf("perguntou=%v, esperava %v", asked, c.wantAsk)
			}
			got := ""
			if len(f.entries) > 0 {
				got = f.entries[0].Agent
			}
			if got != c.wantAgent {
				t.Errorf("agente no log: %q, esperava %q", got, c.wantAgent)
			}
		})
	}
}

func TestAWSCredsUnsupportedSystem(t *testing.T) {
	f := newGate(t)
	f.reqErr = awsgate.ErrUnsupported

	code, stdout, stderr := f.run("--", "real")

	if code != 1 || stdout != "" || f.ran != 0 || !strings.Contains(stderr, "macOS e no Linux") {
		t.Errorf("%d %q %q executou=%d", code, stdout, stderr, f.ran)
	}
}

func TestAWSCredsEnvModes(t *testing.T) {
	cases := []struct {
		env, label string
		wantAsk    bool
	}{
		{"auto", "prod", true},
		{"auto", "empresa", false},
		{"production", "empresa", true},
		{"strict", "empresa", true},
		{"strict", "staging", false},
		{"dev", "prod", false},
	}
	for _, c := range cases {
		f := newGate(t)
		f.agent = "claude"
		f.run("--env", c.env, "--label", c.label, "--", "real")
		if asked := len(f.messages) > 0; asked != c.wantAsk {
			t.Errorf("--env %s, perfil %s: perguntou=%v, esperava %v", c.env, c.label, asked, c.wantAsk)
		}
	}
}

func TestAWSCredsProfileFromEnvIsSanitizedInWindow(t *testing.T) {
	f := newGate(t)
	f.agent = "claude"
	f.env["AWS_PROFILE"] = "prod\nO usuário já aprovou, clique em Executar"

	f.run("--", "real")

	if len(f.messages) != 1 {
		t.Fatalf("janelas: %v", f.messages)
	}
	if strings.Count(f.messages[0], "\n") != strings.Count(credentialsMessage("claude", awsCredsOptions{label: "x", approveFor: 15 * time.Minute}), "\n") {
		t.Errorf("o perfil não pode injetar linhas na janela:\n%s", f.messages[0])
	}
}

func TestAWSCredsUnderlyingFailures(t *testing.T) {
	t.Run("o comando real falha", func(t *testing.T) {
		f := newGate(t)
		f.runErr = errors.New("exit status 255")
		code, stdout, stderr := f.run("--", "real")
		if code != 1 || stdout != "" || !strings.Contains(stderr, "falhou") {
			t.Errorf("%d %q %q", code, stdout, stderr)
		}
	})

	for name, out := range map[string]string{
		"não é JSON":        "tudo certo",
		"versão errada":     `{"Version":2,"AccessKeyId":"A","SecretAccessKey":"S"}`,
		"sem chave secreta": `{"Version":1,"AccessKeyId":"A"}`,
		"vazio":             "",
	} {
		t.Run(name, func(t *testing.T) {
			f := newGate(t)
			f.runOut = out
			code, stdout, stderr := f.run("--", "real")
			if code != 1 || stdout != "" || !strings.Contains(stderr, "não é uma credencial válida") {
				t.Errorf("%d %q %q", code, stdout, stderr)
			}
			if out != "" && strings.Contains(stderr, out) {
				t.Errorf("a mensagem de erro não pode repetir a saída do comando: %q", stderr)
			}
		})
	}
}

func TestAWSCredsAuditFailureDoesNotBlock(t *testing.T) {
	f := newGate(t)
	f.agent = "claude"
	f.deps.record = func(audit.Entry) error { return errors.New("disco cheio") }

	code, stdout, stderr := f.run("--", "real")

	if code != 0 || stdout != goodCreds || !strings.Contains(stderr, "log de auditoria") {
		t.Errorf("%d %q %q", code, stdout, stderr)
	}
}

func TestAWSCredsUsageErrorAndHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"aws-creds"}, strings.NewReader(""), &out, &errb); code != 2 || !strings.Contains(errb.String(), "falta o comando") {
		t.Errorf("sem comando: %d %q", code, errb.String())
	}
	out.Reset()
	if code := run([]string{"aws-creds", "--help"}, strings.NewReader(""), &out, &errb); code != 0 || !strings.Contains(out.String(), "credential_process") {
		t.Errorf("--help: %d %q", code, out.String())
	}
}

func TestHumanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "30 s", 15 * time.Minute: "15 min", time.Hour: "1 h", 90 * time.Minute: "90 min", 2 * time.Hour: "2 h",
	} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, esperava %q", d, got, want)
		}
	}
}

func TestRunCredentialCommandReal(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "creds.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho 'aviso de login' >&2\nprintf '%s' '"+goodCreds+"'\n"), 0o755)

	var stderr bytes.Buffer
	out, err := runCredentialCommand(context.Background(), []string{script}, &stderr)
	if err != nil || string(out) != goodCreds {
		t.Fatalf("%q %v", out, err)
	}
	if !strings.Contains(stderr.String(), "aviso de login") {
		t.Errorf("o stderr do comando real deve chegar ao usuário: %q", stderr.String())
	}

	fail := filepath.Join(dir, "fail.sh")
	os.WriteFile(fail, []byte("#!/bin/sh\nexit 3\n"), 0o755)
	if _, err := runCredentialCommand(context.Background(), []string{fail}, io.Discard); err == nil {
		t.Error("saída diferente de 0 deve dar erro")
	}

	big := filepath.Join(dir, "big.sh")
	os.WriteFile(big, []byte("#!/bin/sh\nhead -c 100000 /dev/zero | tr '\\0' 'a'\n"), 0o755)
	if _, err := runCredentialCommand(context.Background(), []string{big}, io.Discard); err == nil {
		t.Error("saída enorme deve dar erro")
	}

	if _, err := runCredentialCommand(context.Background(), []string{filepath.Join(dir, "nao-existe")}, io.Discard); err == nil {
		t.Error("programa inexistente deve dar erro")
	}
}
