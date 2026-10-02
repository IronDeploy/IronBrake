package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/setup"
)

func noLookPath(string) (string, error) { return "", errors.New("não achei") }

// homeWith cria um home com as pastas dos agentes dados (".kiro", ".codex", ...).
func homeWith(t *testing.T, folders ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	for _, f := range folders {
		if err := os.MkdirAll(filepath.Join(home, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func exists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, rel))
	return err == nil
}

func TestParseInitAgents(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{nil, nil},
		{[]string{"--harden"}, nil},
		{[]string{"--agent=kiro"}, []string{"kiro"}},
		{[]string{"--agent", "codex"}, []string{"codex"}},
		{[]string{"--agent=kiro,codex"}, []string{"kiro", "codex"}},
		{[]string{"--agent=agy", "--agent=KIRO", "--agent=antigravity"}, []string{"antigravity", "kiro"}},
		{[]string{"--agent=claude, kiro,"}, []string{"claude", "kiro"}},
		{[]string{"--agent=ALL"}, []string{"all"}},
	}
	for _, c := range cases {
		got, err := parseInitAgents(c.args)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("parseInitAgents(%v) = %v, %v; quero %v", c.args, got, err, c.want)
		}
	}
	if _, err := parseInitAgents([]string{"--agent=kiro,emacs"}); err == nil {
		t.Error("agente desconhecido na lista deveria dar erro")
	}
}

func TestInitWithoutAgentOnlyClaudeAndHintsWithoutTerminal(t *testing.T) {
	dir := t.TempDir()
	env := initEnv{dir: dir, exePath: testExe, home: homeWith(t, ".claude", ".kiro", ".codex"), lookPath: noLookPath}

	var stdout, stderr bytes.Buffer
	if code := runInitAgents(nil, env, &stdout, &stderr); code != 0 {
		t.Fatalf("código %d: %s", code, stderr.String())
	}
	if !exists(dir, ".claude/settings.json") || exists(dir, ".kiro") || exists(dir, ".codex") {
		t.Error("sem --agent e sem terminal só o Claude é instalado")
	}
	if !strings.Contains(stdout.String(), "também detectado nesta máquina: Kiro CLI, Codex CLI") ||
		!strings.Contains(stdout.String(), "iron init --agent=kiro,codex") {
		t.Errorf("deveria avisar do comando para os outros agentes: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "== ") {
		t.Errorf("com um agente só não há cabeçalho: %q", stdout.String())
	}
}

func TestInitInteractiveAsksOneByOne(t *testing.T) {
	dir := t.TempDir()
	env := initEnv{dir: dir, exePath: testExe, home: homeWith(t, ".kiro", ".codex"), lookPath: noLookPath,
		interactive: true, stdin: strings.NewReader("s\nn\n")}

	var stdout, stderr bytes.Buffer
	if code := runInitAgents(nil, env, &stdout, &stderr); code != 0 {
		t.Fatalf("código %d: %s", code, stderr.String())
	}
	if !exists(dir, ".kiro/hooks/iron-brake.json") || exists(dir, ".codex") || !exists(dir, ".claude/settings.json") {
		t.Errorf("sim ao Kiro, não ao Codex: kiro=%v codex=%v", exists(dir, ".kiro"), exists(dir, ".codex"))
	}
	for _, want := range []string{"Kiro CLI detectado", "Codex CLI detectado", "[s/N]", "== Claude Code ==", "== Kiro CLI =="} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("a saída deveria conter %q:\n%s", want, stdout.String())
		}
	}
}

func TestInitInteractiveEmptyAnswerMeansNo(t *testing.T) {
	dir := t.TempDir()
	env := initEnv{dir: dir, exePath: testExe, home: homeWith(t, ".kiro"), lookPath: noLookPath, interactive: true, stdin: strings.NewReader("\n")}
	var stdout, stderr bytes.Buffer
	runInitAgents(nil, env, &stdout, &stderr)
	if exists(dir, ".kiro") {
		t.Error("Enter sem resposta é não")
	}
}

func TestInitDoesNotAskAgainForInstalledAgent(t *testing.T) {
	dir := t.TempDir()
	if _, err := setup.InstallKiro(dir, testExe); err != nil {
		t.Fatal(err)
	}
	env := initEnv{dir: dir, exePath: testExe, home: homeWith(t, ".kiro"), lookPath: noLookPath, interactive: true, stdin: strings.NewReader("s\n")}
	var stdout, stderr bytes.Buffer
	runInitAgents(nil, env, &stdout, &stderr)
	if strings.Contains(stdout.String(), "detectado") {
		t.Errorf("o Kiro já está instalado aqui, não deveria perguntar: %q", stdout.String())
	}
}

func TestInitExplicitList(t *testing.T) {
	dir := t.TempDir()
	env := initEnv{dir: dir, exePath: testExe, home: homeWith(t), lookPath: noLookPath}

	var stdout, stderr bytes.Buffer
	if code := runInitAgents([]string{"--agent=kiro,codex"}, env, &stdout, &stderr); code != 0 {
		t.Fatalf("código %d: %s", code, stderr.String())
	}
	if !exists(dir, ".kiro/hooks/iron-brake.json") || !exists(dir, ".codex/hooks.json") || exists(dir, ".claude") {
		t.Error("só os agentes da lista, sem o Claude")
	}
	if !strings.Contains(stdout.String(), "== Kiro CLI ==") || !strings.Contains(stdout.String(), "== Codex CLI ==") {
		t.Errorf("com vários agentes cada um tem cabeçalho: %q", stdout.String())
	}
}

func TestInitAll(t *testing.T) {
	dir := t.TempDir()
	env := initEnv{dir: dir, exePath: testExe, home: homeWith(t, ".claude", ".gemini/antigravity-cli", ".codex"), lookPath: noLookPath}

	var stdout, stderr bytes.Buffer
	if code := runInitAgents([]string{"--agent=all"}, env, &stdout, &stderr); code != 0 {
		t.Fatalf("código %d: %s", code, stderr.String())
	}
	for _, f := range []string{".claude/settings.json", ".agents/hooks.json", ".codex/hooks.json"} {
		if !exists(dir, f) {
			t.Errorf("--agent=all deveria instalar em %s", f)
		}
	}
	if exists(dir, ".kiro") {
		t.Error("o Kiro não foi detectado: não deve ser instalado")
	}

	empty := initEnv{dir: t.TempDir(), exePath: testExe, home: homeWith(t), lookPath: noLookPath}
	stderr.Reset()
	if code := runInitAgents([]string{"--agent=all"}, empty, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "nenhum agente") {
		t.Errorf("sem agentes detectados deveria sair 2: %d %q", code, stderr.String())
	}
}

func TestInitHardenAppliesOnlyToClaude(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := initEnv{dir: t.TempDir(), exePath: testExe, home: homeWith(t), lookPath: noLookPath}

	// só agentes que não são o Claude: erro, como antes.
	if code := runInitAgents([]string{"--agent=kiro,codex", "--harden"}, env, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "inclua claude") {
		t.Errorf("--harden sem o Claude deveria sair 2: %d %q", code, stderr.String())
	}

	// com o Claude na lista, o shield vai só nele.
	dir := t.TempDir()
	env.dir = dir
	stderr.Reset()
	if code := runInitAgents([]string{"--agent=claude,kiro", "--harden"}, env, &stdout, &stderr); code != 0 {
		t.Fatalf("código %d: %s", code, stderr.String())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".claude/settings.json")); !strings.Contains(string(data), "Read(") {
		t.Errorf("o shield deveria estar no settings.json do Claude: %s", data)
	}
	if !exists(dir, ".kiro/hooks/iron-brake.json") {
		t.Error("o Kiro também deveria ser instalado")
	}
}

func TestInitContinuesAfterOneAgentFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".kiro/agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kiro/agents/ruim.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := initEnv{dir: dir, exePath: testExe, home: homeWith(t), lookPath: noLookPath}

	var stdout, stderr bytes.Buffer
	code := runInitAgents([]string{"--agent=kiro,codex"}, env, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "ruim.json") {
		t.Errorf("a falha do Kiro deveria dar código 1 citando o arquivo: %d %q", code, stderr.String())
	}
	if !exists(dir, ".codex/hooks.json") {
		t.Error("o Codex deveria ser instalado mesmo com a falha do Kiro")
	}
}

func TestRunStatus(t *testing.T) {
	setHome(t, t.TempDir())
	t.Setenv("CODEX_HOME", "")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("código %d: %s", code, stderr.String())
	}
	for _, want := range []string{"iron status:", "Claude Code", "Kiro CLI", "Antigravity CLI", "Codex CLI", "Protegendo esta pasta"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("a saída deveria conter %q:\n%s", want, stdout.String())
		}
	}
}

// /dev/null é um dispositivo de caracteres, mas não é um terminal: perguntar nele
// travaria ou responderia sozinho.
func TestIsTerminal(t *testing.T) {
	if isTerminal(strings.NewReader("")) {
		t.Error("um Reader qualquer não é terminal")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if isTerminal(null) {
		t.Error("/dev/null não é terminal")
	}
	file, err := os.Open(filepath.Join(t.TempDir(), "..", "."))
	if err == nil {
		defer file.Close()
		if isTerminal(file) {
			t.Error("uma pasta não é terminal")
		}
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	if isTerminal(pr) {
		t.Error("um pipe não é terminal")
	}
}
