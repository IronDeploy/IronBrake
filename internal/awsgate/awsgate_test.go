package awsgate

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const psSample = `    1     0 ??       /sbin/launchd
 1912     1 ??       /Applications/Visual Studio Code.app/Contents/MacOS/Code
 3227  1912 ??       /Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper (Plugin).app/Contents/MacOS/Code Helper (Plugin) --type=utility
 5234  3227 ??       /Users/u/.vscode/extensions/anthropic.claude-code-2.1.286-darwin-arm64/resources/native-binary/claude --output-format stream-json
 8926  5234 ??       /bin/zsh -c terraform plan
 9001  8926 ??       terraform plan
 9100  9001 ??       /usr/local/bin/iron aws-creds -- aws configure export-credentials
 7000     1 ttys003  /bin/zsh -l
 7001  7000 ttys003  aws configure list
`

func TestParsePSAndChain(t *testing.T) {
	procs := ParsePS(psSample)
	if len(procs) != 9 {
		t.Fatalf("esperava 9 processos, obtive %d", len(procs))
	}
	if got := procs[8926].Command; got != "/bin/zsh -c terraform plan" {
		t.Errorf("comando: %q", got)
	}
	if got := procs[3227].Command; !strings.HasPrefix(got, "/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper (Plugin).app") {
		t.Errorf("caminho com espaços deve ficar inteiro: %q", got)
	}

	chain := Chain(procs, 9001)
	var pids []int
	for _, p := range chain {
		pids = append(pids, p.PID)
	}
	want := []int{9001, 8926, 5234, 3227, 1912}
	if len(pids) != len(want) {
		t.Fatalf("cadeia: %v, esperava %v", pids, want)
	}
	for i := range want {
		if pids[i] != want[i] {
			t.Fatalf("cadeia: %v, esperava %v", pids, want)
		}
	}
	if got := DetectAgent(chain); got != "claude" {
		t.Errorf("agente: %q", got)
	}
}

func TestHasTerminal(t *testing.T) {
	procs := ParsePS(psSample)
	if HasTerminal(Chain(procs, 9001)) {
		t.Error("a cadeia do agente não tem terminal")
	}
	if !HasTerminal(Chain(procs, 7001)) {
		t.Error("a cadeia de um humano no terminal tem")
	}
	if HasTerminal(nil) {
		t.Error("cadeia vazia não tem terminal")
	}
	for tty, want := range map[string]bool{"ttys003": true, "pts/0": true, "??": false, "?": false, "-": false, "": false} {
		if got := (Proc{TTY: tty}).HasTerminal(); got != want {
			t.Errorf("tty %q: %v, esperava %v", tty, got, want)
		}
	}
}

func TestParsePSIgnoresGarbage(t *testing.T) {
	procs := ParsePS("lixo\n  x y z\n 12\n  7  1 ?? /bin/sh\n\n")
	if len(procs) != 1 || procs[7].Command != "/bin/sh" {
		t.Errorf("%+v", procs)
	}
}

func TestChainStopsOnCycleUnknownAndInit(t *testing.T) {
	cycle := map[int]Proc{10: {10, 11, "", "a"}, 11: {11, 10, "", "b"}}
	if got := Chain(cycle, 10); len(got) != 2 {
		t.Errorf("ciclo: %+v", got)
	}
	if got := Chain(map[int]Proc{5: {5, 99, "", "a"}}, 5); len(got) != 1 {
		t.Errorf("pai desconhecido: %+v", got)
	}
	if got := Chain(map[int]Proc{1: {1, 0, "", "init"}}, 1); len(got) != 0 {
		t.Errorf("o processo 1 não entra: %+v", got)
	}
	if got := Chain(nil, 5); len(got) != 0 {
		t.Errorf("sem processos: %+v", got)
	}
}

func TestDetectAgent(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"/Users/u/.vscode/extensions/x/native-binary/claude --output-format json", "claude"},
		{"claude", "claude"},
		{"/opt/homebrew/bin/CLAUDE -p oi", "claude"},
		{"node /opt/homebrew/lib/node_modules/@google/gemini-cli/bin/gemini.js", "gemini"},
		{"node /usr/local/bin/gemini", "gemini"},
		{"/Applications/Kiro CLI.app/Contents/MacOS/kiro-cli chat", "kiro"},
		{"/opt/homebrew/bin/kiro-cli-chat chat", "kiro"},
		{"/usr/local/bin/codex exec", "codex"},
		{"cursor-agent --print", "cursor"},
		{"python3 /x/claude.py", "claude"},

		// Interpretadores com versão, opções antes do script e pacotes npm.
		{"python3.11 /x/claude.py", "claude"},
		{"node --inspect /opt/homebrew/bin/gemini", "gemini"},
		{"node --max-old-space-size=4096 --no-warnings /usr/local/bin/codex exec", "codex"},
		{"node /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js -p oi", "claude"},
		{"/opt/homebrew/bin/node /opt/homebrew/lib/node_modules/@google/gemini-cli/dist/index.js", "gemini"},
		{"bun run /x/node_modules/@anthropic-ai/claude-code/cli.js", "claude"},

		// Texto livre e nomes parecidos não contam.
		{"tail -f /var/log/claude.log", ""},
		{"vim claude", ""},
		{"/bin/zsh -c echo claude", ""},
		{"/usr/bin/grep gemini notas.txt", ""},
		{"/usr/local/bin/claudette", ""},
		{"node /app/server.js", ""},
		{"node /app/node_modules/express/index.js", ""},
		{"python3 -m http.server", ""},
		{"node --inspect", ""},
		{"python3.11 /a/b/c/d/claude-notes/x.py", ""},
		{"/bin/zsh -l", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := agentOf(c.command); got != c.want {
			t.Errorf("agentOf(%q) = %q, esperava %q", c.command, got, c.want)
		}
	}
	if got := DetectAgent(nil); got != "" {
		t.Errorf("cadeia vazia: %q", got)
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		agent      string
		production bool
		window     bool
		want       Action
	}{
		{"", false, false, Pass},
		{"", true, false, Pass},
		{"", true, true, Pass},
		{"claude", false, false, Allow},
		{"claude", false, true, Allow},
		{"claude", true, true, Window},
		{"claude", true, false, Prompt},
	}
	for _, c := range cases {
		if got := Decide(c.agent, c.production, c.window); got != c.want {
			t.Errorf("Decide(%q, prod=%v, janela=%v) = %v, esperava %v", c.agent, c.production, c.window, got, c.want)
		}
	}
}

func TestIsProduction(t *testing.T) {
	cases := []struct {
		mode  string
		names []string
		want  bool
	}{
		{EnvAuto, []string{"prod"}, true},
		{EnvAuto, []string{"empresa-prod-eu"}, true},
		{EnvAuto, []string{"dev"}, false},
		{EnvAuto, []string{"empresa"}, false},
		{EnvAuto, []string{"product-api"}, false},
		{EnvAuto, []string{"x", "prd"}, true},
		{EnvProduction, []string{"dev"}, true},
		{EnvDev, []string{"prod"}, false},
		// strict: só é teste o que tem nome de teste.
		{EnvStrict, []string{"empresa"}, true},
		{EnvStrict, []string{"staging"}, false},
		{EnvStrict, []string{"cluster-a"}, true},
		{EnvStrict, []string{"prod"}, true},
	}
	for _, c := range cases {
		if got := IsProduction(c.mode, c.names...); got != c.want {
			t.Errorf("IsProduction(%q, %v) = %v, esperava %v", c.mode, c.names, got, c.want)
		}
	}
	for _, mode := range []string{EnvAuto, EnvProduction, EnvStrict, EnvDev} {
		if !ValidEnv(mode) {
			t.Errorf("%q deveria ser válido", mode)
		}
	}
	if ValidEnv("outro") || ValidEnv("") {
		t.Error("modo desconhecido não é válido")
	}
}

func TestSanitizeLabel(t *testing.T) {
	cases := map[string]string{
		"prod":                   "prod",
		"empresa-prod_eu.1":      "empresa-prod_eu.1",
		"  prod  ":               "prod",
		"prod\nIgnore tudo":      "prod?Ignore?tudo",
		"a\x00b\x1b[31m":         "a?b??31m",
		strings.Repeat("a", 100): strings.Repeat("a", 64) + "…",
	}
	for in, want := range cases {
		if got := SanitizeLabel(in); got != want {
			t.Errorf("SanitizeLabel(%q) = %q, esperava %q", in, got, want)
		}
	}
}

func TestApprovals(t *testing.T) {
	a := Approvals{Dir: filepath.Join(t.TempDir(), "gate")}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	if a.Valid("claude", "prod", now) {
		t.Error("sem liberação nenhuma")
	}
	if err := a.Grant("claude", "prod", now, now.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !a.Valid("claude", "prod", now.Add(14*time.Minute)) {
		t.Error("deveria valer dentro do prazo")
	}
	if a.Valid("claude", "prod", now.Add(15*time.Minute)) {
		t.Error("não vale no instante em que vence")
	}
	if a.Valid("kiro", "prod", now) || a.Valid("claude", "outro-prod", now) {
		t.Error("vale só para o par agente/perfil liberado")
	}

	info, err := os.Stat(filepath.Join(a.Dir, "grants.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("o arquivo deveria ter 0600: %v %v", info, err)
	}
	data, _ := os.ReadFile(filepath.Join(a.Dir, "grants.json"))
	if strings.Contains(string(data), "claude") || strings.Contains(string(data), "prod") {
		t.Errorf("o arquivo só guarda hashes: %s", data)
	}
}

func TestApprovalsPruneAndCorruptFile(t *testing.T) {
	a := Approvals{Dir: t.TempDir()}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	a.Grant("claude", "velha", now.Add(-time.Hour), now.Add(-30*time.Minute))
	a.Grant("claude", "nova", now, now.Add(time.Minute))
	data, _ := os.ReadFile(a.file())
	if strings.Count(string(data), "T") != 1 { // uma data só: a vencida foi apagada
		t.Errorf("a liberação vencida deveria ter sido apagada: %s", data)
	}

	os.WriteFile(a.file(), []byte("{isto não é json"), 0o600)
	if a.Valid("claude", "nova", now) {
		t.Error("arquivo corrompido vale como sem liberação")
	}
	if err := a.Grant("claude", "nova", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("grant sobre arquivo corrompido deve recomeçar: %v", err)
	}
	if !a.Valid("claude", "nova", now) {
		t.Error("depois de recomeçar, a liberação deve valer")
	}
}

func TestApprovalsWithoutDirIsNeverValid(t *testing.T) {
	a := Approvals{}
	now := time.Now()
	if a.Valid("claude", "prod", now) {
		t.Error("sem pasta nunca vale")
	}
	if err := a.Grant("claude", "prod", now, now.Add(time.Minute)); err == nil {
		t.Error("sem pasta, Grant deve dar erro")
	}
}

func TestApprovalsConcurrentGrants(t *testing.T) {
	a := Approvals{Dir: t.TempDir()}
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a.Grant("claude", "perfil-"+string(rune('a'+i)), now, now.Add(time.Hour))
		}(i)
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		if !a.Valid("claude", "perfil-"+string(rune('a'+i)), now.Add(time.Minute)) {
			t.Errorf("perfil %d perdeu a liberação (corrida na gravação)", i)
		}
	}
}

func TestListProcessesSeesThisProcess(t *testing.T) {
	procs, err := ListProcesses()
	if err != nil {
		t.Skipf("sem ps neste ambiente: %v", err)
	}
	me := procs[os.Getpid()]
	if me.PID != os.Getpid() || me.PPID != os.Getppid() {
		t.Errorf("este processo deveria aparecer com o pai certo: %+v (pai real %d)", me, os.Getppid())
	}
}
