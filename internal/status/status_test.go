package status

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/audit"
	"github.com/IronDeploy/IronBrake/internal/setup"
)

var now = time.Date(2026, 10, 1, 22, 0, 0, 0, time.UTC)

func noPath(string) (string, error) { return "", errors.New("não achei") }

// fakeIron cria um arquivo chamado iron (o status só confere que ele existe).
func fakeIron(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bin", "iron")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func input(t *testing.T, dir string) Input {
	t.Helper()
	home := t.TempDir()
	return Input{Dir: dir, Home: home, CodexConfig: filepath.Join(home, ".codex", "config.toml"), LookPath: noPath, Now: now}
}

func byName(agents []Agent, name string) Agent {
	for _, a := range agents {
		if a.Name == name {
			return a
		}
	}
	return Agent{}
}

func installAll(t *testing.T, dir, exe string) {
	t.Helper()
	if _, err := setup.InstallHook(setup.SettingsPath(dir), exe); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.InstallKiro(dir, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.InstallAntigravity(dir, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.InstallCodex(dir, exe); err != nil {
		t.Fatal(err)
	}
}

func trustCodex(t *testing.T, in Input) {
	t.Helper()
	body := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n\n[hooks.state.%q]\ntrusted_hash = \"sha256:abc\"\n", in.Dir, setup.CodexHooksPath(in.Dir)+":pre_tool_use:0:0")
	if err := os.MkdirAll(filepath.Dir(in.CodexConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.CodexConfig, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(in.CodexConfig, later, later); err != nil {
		t.Fatal(err)
	}
}

func TestCollectNothingInstalled(t *testing.T) {
	in := input(t, t.TempDir())
	agents := Collect(in)
	if len(agents) != 4 {
		t.Fatalf("esperava 4 agentes, obtive %d", len(agents))
	}
	for _, a := range agents {
		if a.Installed || a.Detected || len(a.Warnings) != 0 || a.Last != nil {
			t.Errorf("nada deveria estar instalado: %+v", a)
		}
	}
	for i, name := range []string{"claude", "kiro", "antigravity", "codex"} {
		if agents[i].Name != name {
			t.Errorf("ordem: posição %d é %s, quero %s", i, agents[i].Name, name)
		}
	}
}

func TestCollectAllInstalledAndHealthy(t *testing.T) {
	dir := t.TempDir()
	in := input(t, dir)
	installAll(t, dir, fakeIron(t))
	trustCodex(t, in)

	for _, a := range Collect(in) {
		if !a.Installed || len(a.Warnings) != 0 || a.Where == "" {
			t.Errorf("%s deveria estar instalado e sem avisos: %+v", a.Name, a)
		}
	}
}

func TestCollectDetectedButNotInstalled(t *testing.T) {
	in := input(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(in.Home, ".kiro"), 0o755); err != nil {
		t.Fatal(err)
	}
	k := byName(Collect(in), "kiro")
	if !k.Detected || k.Installed {
		t.Errorf("kiro detectado e não instalado: %+v", k)
	}
}

func TestCollectCodexUntrustedDoesNotProtect(t *testing.T) {
	dir := t.TempDir()
	in := input(t, dir)
	installAll(t, dir, fakeIron(t))

	c := byName(Collect(in), "codex")
	if !c.Unprotected {
		t.Error("hook não confiado não protege")
	}
	if !c.Installed || len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "NÃO PROTEGE") || !strings.Contains(c.Warnings[0], "pasta") {
		t.Errorf("sem confiança na pasta: %+v", c)
	}

	// pasta confiada, hook não.
	body := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir)
	if err := os.MkdirAll(filepath.Dir(in.CodexConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.CodexConfig, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c = byName(Collect(in), "codex")
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "hook ainda não foi confiado") {
		t.Errorf("sem confiança no hook: %+v", c)
	}
}

func TestCollectWarnings(t *testing.T) {
	dir := t.TempDir()
	in := input(t, dir)
	installAll(t, dir, fakeIron(t))
	trustCodex(t, in)

	// antigravity: hooks.json quebrado.
	if err := os.WriteFile(setup.AntigravityHooksPath(dir), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	// kiro: sem o hook do v3.
	if err := os.Remove(filepath.Join(dir, ".kiro/hooks/iron-brake.json")); err != nil {
		t.Fatal(err)
	}
	agents := Collect(in)

	if a := byName(agents, "antigravity"); a.Installed || len(a.Warnings) != 1 || !strings.Contains(a.Warnings[0], "SEM avisar") {
		t.Errorf("antigravity com JSON quebrado: %+v", a)
	}
	if k := byName(agents, "kiro"); !k.Installed || len(k.Warnings) != 1 || !strings.Contains(k.Warnings[0], "Kiro v3") {
		t.Errorf("kiro sem o hook do v3: %+v", k)
	}
}

func TestCollectMissingBinaryWarns(t *testing.T) {
	dir := t.TempDir()
	in := input(t, dir)
	installAll(t, dir, filepath.Join(t.TempDir(), "iron")) // o arquivo não existe
	trustCodex(t, in)
	for _, a := range Collect(in) {
		if !a.Unprotected || !a.Installed || len(a.Warnings) == 0 || !strings.Contains(strings.Join(a.Warnings, " "), "não existe") {
			t.Errorf("%s deveria avisar do programa que não existe: %+v", a.Name, a)
		}
	}
}

func TestLastDecisionPerAgent(t *testing.T) {
	dir := t.TempDir()
	in := input(t, dir)
	e := func(agent, class, decision string, minutesAgo int) audit.Entry {
		return audit.Entry{Time: now.Add(-time.Duration(minutesAgo) * time.Minute), Agent: agent, Class: class, Decision: decision, Rule: "r"}
	}
	in.Entries = func(since time.Time) ([]audit.Entry, error) {
		return []audit.Entry{
			e("", "git status", "allow", 50), // log antigo, sem campo agent: é do claude
			e("claude", "git push", "deny", 20),
			e("kiro", "terraform apply", "deny", 30),
			e("kiro", "git status", "allow", 40),
			{Time: now.Add(-time.Minute), Agent: "kiro", Class: "git push", Decision: "gap", Rule: "watch"}, // o watch não é decisão
		}, nil
	}
	agents := Collect(in)

	if l := byName(agents, "claude").Last; l == nil || l.Class != "git push" {
		t.Errorf("claude: a última é o git push de 20 min atrás: %+v", l)
	}
	if l := byName(agents, "kiro").Last; l == nil || l.Class != "terraform apply" {
		t.Errorf("kiro: ignora o gap do watch: %+v", l)
	}
	if byName(agents, "codex").Last != nil {
		t.Error("codex não tem decisões")
	}
}

func TestPrint(t *testing.T) {
	dir := t.TempDir()
	in := input(t, dir)
	installAll(t, dir, fakeIron(t))
	if err := os.MkdirAll(filepath.Join(in.Home, ".gemini", "antigravity-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(setup.AntigravityHooksPath(dir))
	in.Entries = func(time.Time) ([]audit.Entry, error) {
		return []audit.Entry{{Time: now, Agent: "claude", Class: "git push", Decision: "deny", Rule: "git-force-push"}}, nil
	}

	var out bytes.Buffer
	Print(&out, dir, Collect(in), now)
	text := out.String()
	for _, want := range []string{
		"Claude Code", "instalado em .claude/settings.json", "verificado com Claude Code 2.1.286",
		"git push → deny (git-force-push)",
		"Antigravity CLI", "detectado nesta máquina, não instalado aqui: iron init --agent=antigravity",
		"NÃO PROTEGE", "Protegendo esta pasta: 2 de 4", "iron doctor --agent=NOME",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("a saída deveria conter %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Kiro CLI") && !strings.Contains(line, "verificado com") && !strings.HasPrefix(line, "OK ") {
			t.Errorf("o Kiro saudável deveria aparecer como OK: %q", line)
		}
	}
}

func TestInstalledHelper(t *testing.T) {
	dir := t.TempDir()
	if Installed(dir, "kiro") {
		t.Error("nada instalado")
	}
	if _, err := setup.InstallKiro(dir, fakeIron(t)); err != nil {
		t.Fatal(err)
	}
	if !Installed(dir, "kiro") || Installed(dir, "codex") {
		t.Error("só o kiro está instalado")
	}
}
