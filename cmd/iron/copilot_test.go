package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/dialog"
	"github.com/IronDeploy/IronBrake/internal/hook"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

// copilotPayload monta o stdin no formato real do Copilot CLI 1.0.112.
func copilotPayload(t *testing.T, tool, command string) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"sessionId": "62b46c1b-54d4-4521-ae91-998e15cfeb59", "timestamp": 1794755566446, "cwd": "/home/dev/projeto",
		"toolCallId": "call_1", "toolName": tool, "toolArgs": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func copilotDeps(d *fakeDialog, log *fakeAudit) hookDeps {
	deps := testDeps()
	deps.agent = hook.Copilot
	deps.confirm, deps.audit = d.confirm, log.append
	return deps
}

func TestRunHookCopilotBlocksAndAllows(t *testing.T) {
	t.Run("bloqueia force push com JSON deny", func(t *testing.T) {
		log := &fakeAudit{}
		var stdout, stderr bytes.Buffer
		code := runHook(copilotPayload(t, "bash", "git push --force origin main"), &stdout, &stderr, copilotDeps(&fakeDialog{}, log))
		var out map[string]string
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || code != 0 ||
			out["permissionDecision"] != "deny" || !strings.Contains(out["permissionDecisionReason"], "force push") {
			t.Errorf("código %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
		}
		if len(log.entries) != 1 || log.entries[0].Agent != "copilot" || log.entries[0].Session != "62b46c1b-54d4-4521-ae91-998e15cfeb59" {
			t.Errorf("o log deveria trazer agent=copilot e a sessão: %+v", log.entries)
		}
	})

	t.Run("comando seguro: silêncio total", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(copilotPayload(t, "bash", "git status"), &stdout, &stderr, copilotDeps(&fakeDialog{}, &fakeAudit{}))
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("allow deveria ser silencioso: %d %q %q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("outra ferramenta não é analisada como shell", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(copilotPayload(t, "edit", "git push --force origin main"), &stdout, &stderr, copilotDeps(&fakeDialog{}, &fakeAudit{}))
		if code != 0 || stdout.Len() != 0 {
			t.Errorf("edit deveria passar em silêncio: %d %q %q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("payload quebrado bloqueia, nunca libera", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runHook(strings.NewReader("{"), &stdout, &stderr, copilotDeps(&fakeDialog{}, &fakeAudit{}))
		if code == 0 && !strings.Contains(stdout.String(), `"permissionDecision":"deny"`) {
			t.Errorf("deveria bloquear: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
}

// Sem verificação do "ask" no agente real, o Iron Brake pergunta na janela e,
// sem janela, bloqueia.
func TestRunHookCopilotAskNeedsTheWindow(t *testing.T) {
	cmd := "git reset --hard"
	t.Run("janela aprova: libera em silêncio", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		d := &fakeDialog{answer: dialog.Approved}
		code := runHook(copilotPayload(t, "bash", cmd), &stdout, &stderr, copilotDeps(d, &fakeAudit{}))
		if code != 0 || stdout.Len() != 0 {
			t.Errorf("aprovado deveria liberar: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("janela indisponível: deny", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		d := &fakeDialog{answer: dialog.Unavailable}
		code := runHook(copilotPayload(t, "bash", cmd), &stdout, &stderr, copilotDeps(d, &fakeAudit{}))
		if code != 0 || !strings.Contains(stdout.String(), `"permissionDecision":"deny"`) {
			t.Errorf("sem janela deveria bloquear: %d %q %q", code, stdout.String(), stderr.String())
		}
	})
}

// isolateCopilotHome aponta HOME e COPILOT_HOME para uma pasta de teste, para o
// init nunca ler a configuração real do Copilot.
func isolateCopilotHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("COPILOT_HOME", "")
	return home
}

func TestRunInitCopilot(t *testing.T) {
	isolateCopilotHome(t)
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := runInitCopilot(dir, testExe, initOptions{awsTag: true}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "instalado") ||
		!strings.Contains(stdout.String(), "AWS_SDK_UA_APP_ID=iron-copilot") {
		t.Fatalf("código %d: %q %q", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, ".github/hooks/iron-brake.json"))
	if err != nil || !strings.Contains(string(data), "hook --agent=copilot") || !strings.Contains(string(data), `"timeoutSec": 600`) {
		t.Errorf("o arquivo deveria existir e chamar o iron: %v %s", err, data)
	}
	// a pessoa precisa saber o que escolher, e que o -p não pergunta.
	for _, want := range []string{"NÃO roda este hook até você confiar na pasta", "copilot -p não pergunta", "remember this folder"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("deveria explicar a confiança (%q): %q", want, stdout.String())
		}
	}
	for _, other := range []string{".claude", ".kiro", ".agents", ".codex"} {
		if _, err := os.Stat(filepath.Join(dir, other)); err == nil {
			t.Errorf("não deve criar %s", other)
		}
	}

	stdout.Reset()
	if code := runInitCopilot(dir, testExe, initOptions{}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "já estava") {
		t.Errorf("segunda vez: %d %q", code, stdout.String())
	}
	stdout.Reset()
	if code := runInitCopilot(dir, "/outro/iron", initOptions{}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "atualizado") {
		t.Errorf("caminho novo: %d %q", code, stdout.String())
	}
}

func TestRunInitCopilotAlreadyTrusted(t *testing.T) {
	home := isolateCopilotHome(t)
	dir := t.TempDir()
	cfgDir := filepath.Join(home, ".copilot")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("// This file is managed automatically.\n{\"trustedFolders\":[%q]}\n", dir)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runInitCopilot(dir, testExe, initOptions{}, &stdout, &stderr); code != 0 {
		t.Fatalf("código %d: %q %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "já está confiada") || strings.Contains(stdout.String(), "NÃO roda") {
		t.Errorf("pasta confiada não deveria alarmar: %q", stdout.String())
	}
}

func TestRunInitCopilotHardenIsClaudeOnly(t *testing.T) {
	isolateCopilotHome(t)
	var stdout, stderr bytes.Buffer
	if code := runInitCopilot(t.TempDir(), testExe, initOptions{harden: true}, &stdout, &stderr); code != 2 {
		t.Errorf("--harden deveria sair 2, obtive %d", code)
	}
}

// O Copilot só lê .github/hooks da raiz do repositório: o init numa subpasta
// recusa, em vez de gravar um hook que não roda.
func TestRunInitCopilotRefusesSubfolder(t *testing.T) {
	isolateCopilotHome(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runInitCopilot(sub, testExe, initOptions{}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), root) {
		t.Errorf("deveria recusar citando a raiz: %d %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(sub, ".github")); err == nil {
		t.Error("não pode gravar o hook na subpasta")
	}
	// na raiz instala normalmente.
	if code := runInitCopilot(root, testExe, initOptions{}, &stdout, &stderr); code != 0 {
		t.Errorf("na raiz deveria instalar: %d %q", code, stderr.String())
	}
}

// O Copilot executa o hook de .claude/settings.json sem o "args": o iron roda sem
// argumentos. Isso não pode negar tudo nem analisar o comando duas vezes.
func underCopilot(t *testing.T, project string) {
	t.Helper()
	setHome(t, t.TempDir())
	t.Setenv("COPILOT_CLI", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", project)
}

func TestBareUnderCopilotWithCopilotHookIsSilent(t *testing.T) {
	project := t.TempDir()
	if _, err := setup.InstallCopilot(project, testExe); err != nil {
		t.Fatal(err)
	}
	underCopilot(t, project)
	var stdout, stderr bytes.Buffer
	// até um force push: o hook do Copilot da pasta é quem o analisa.
	code := run(nil, openEvent(t, "git-push-force.json"), &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("deveria sair em silêncio: %d %q %q", code, stdout.String(), stderr.String())
	}
	if entries, _ := (audit.Log{Path: audit.DefaultPath()}).Entries(time.Time{}); len(entries) != 0 {
		t.Errorf("não pode gravar no log (o hook do Copilot já grava): %+v", entries)
	}
}

func TestBareUnderCopilotWithoutCopilotHookAnalyzes(t *testing.T) {
	project := t.TempDir() // sem .github/hooks: só o hook do Claude protege
	underCopilot(t, project)
	var stdout, stderr bytes.Buffer
	if code := run(nil, openEvent(t, "git-push-force.json"), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "force push") {
		t.Errorf("deveria bloquear o force push como o iron hook: %d %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(nil, openEvent(t, "git-status.json"), &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Errorf("git status deveria passar: %d %q %q", code, stdout.String(), stderr.String())
	}
}

// Fora do Copilot, ou num terminal, o iron sem argumentos continua mostrando o uso.
func TestBareOutsideCopilotStillShowsUsage(t *testing.T) {
	t.Setenv("COPILOT_CLI", "")
	var stdout, stderr bytes.Buffer
	if code := run(nil, strings.NewReader(""), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "uso: iron") {
		t.Errorf("deveria mostrar o uso: %d %q", code, stderr.String())
	}
}
