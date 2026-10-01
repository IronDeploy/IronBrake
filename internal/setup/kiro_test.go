package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKiroCommandQuoting(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/iron":      `'/usr/local/bin/iron' hook --agent=kiro`,
		"/Users/a b/bin/iron":      `'/Users/a b/bin/iron' hook --agent=kiro`,
		"/Users/o'neil/bin/iron":   `'/Users/o'\''neil/bin/iron' hook --agent=kiro`,
		"/Users/x/$HOME/`id`/iron": "'/Users/x/$HOME/`id`/iron' hook --agent=kiro",
	}
	for in, want := range cases {
		if got := kiroCommand(in); got != want {
			t.Errorf("kiroCommand(%q) = %q, quero %q", in, got, want)
		}
	}
}

func TestInstallKiroFromScratch(t *testing.T) {
	dir := t.TempDir()
	res, err := InstallKiro(dir, "/opt/iron")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 2 || len(res.Already) != 0 {
		t.Fatalf("resultado inesperado: %+v", res)
	}
	cmd := `'/opt/iron' hook --agent=kiro`

	// v2: agente padrão do projeto com o hook embutido, matcher de shell e prazo alto.
	agent := readJSON(t, filepath.Join(dir, ".kiro/agents/kiro_default.json"))
	pre := agent["hooks"].(map[string]any)["preToolUse"].([]any)[0].(map[string]any)
	if agent["name"] != "kiro_default" || pre["matcher"] != "shell" || pre["command"] != cmd || pre["timeout_ms"] != float64(600000) {
		t.Errorf("agente v2 inesperado: %v", agent)
	}

	// v3: .kiro/hooks com regex válida (o "*" do v2 não compila) e timeout em segundos.
	h := readJSON(t, filepath.Join(dir, ".kiro/hooks/iron-brake.json"))
	entry := h["hooks"].([]any)[0].(map[string]any)
	if h["version"] != "v1" || entry["trigger"] != "PreToolUse" || entry["matcher"] != "^(execute_bash|shell|execute_cmd)$" ||
		entry["timeout"] != float64(600) || entry["enabled"] != true || entry["action"].(map[string]any)["command"] != cmd {
		t.Errorf("hook v3 inesperado: %v", h)
	}
}

func TestInstallKiroIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallKiro(dir, "/opt/iron"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, ".kiro/agents/kiro_default.json"))

	res, err := InstallKiro(dir, "/opt/iron")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 0 || len(res.Already) != 2 {
		t.Errorf("segunda execução não deveria mudar nada: %+v", res)
	}
	after, _ := os.ReadFile(filepath.Join(dir, ".kiro/agents/kiro_default.json"))
	if string(before) != string(after) {
		t.Error("o arquivo mudou na segunda execução")
	}
}

func TestInstallKiroMergesIntoExistingAgents(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".kiro/agents/meu.json"),
		`{"name":"meu","prompt":"oi","hooks":{"userPromptSubmit":[{"command":"echo x"}],"preToolUse":[{"matcher":"write","command":"fmt.sh"}]}}`)
	// kiro_default já existe: nenhum arquivo novo deve ser criado no lugar dele.
	writeFile(t, filepath.Join(dir, ".kiro/agents/padrao.json"), `{"name":"kiro_default","prompt":"meu prompt"}`)
	// agente em formato v3: fica como está.
	writeFile(t, filepath.Join(dir, ".kiro/agents/v3.json"), `{"name":"v3","hooks":[{"name":"x","trigger":"preToolUse"}]}`)
	v3Before, _ := os.ReadFile(filepath.Join(dir, ".kiro/agents/v3.json"))

	res, err := InstallKiro(dir, "/opt/iron")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || !strings.HasSuffix(res.Skipped[0], "v3.json") {
		t.Errorf("agente v3 deveria ser pulado: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, ".kiro/agents/kiro_default.json")); err == nil {
		t.Error("não deveria criar kiro_default.json quando o padrão já existe em outro arquivo")
	}

	meu := readJSON(t, filepath.Join(dir, ".kiro/agents/meu.json"))
	hooks := meu["hooks"].(map[string]any)
	pre := hooks["preToolUse"].([]any)
	if meu["prompt"] != "oi" || len(hooks["userPromptSubmit"].([]any)) != 1 || len(pre) != 2 {
		t.Errorf("o conteúdo da pessoa deveria ser mantido: %v", meu)
	}
	if pre[0].(map[string]any)["command"] != "fmt.sh" {
		t.Error("o hook anterior deve continuar primeiro")
	}

	padrao := readJSON(t, filepath.Join(dir, ".kiro/agents/padrao.json"))
	if padrao["prompt"] != "meu prompt" || padrao["hooks"] == nil {
		t.Errorf("o padrão da pessoa deveria receber o hook sem perder o prompt: %v", padrao)
	}

	v3After, _ := os.ReadFile(filepath.Join(dir, ".kiro/agents/v3.json"))
	if string(v3Before) != string(v3After) {
		t.Error("agente v3 não deve ser alterado")
	}
}

func TestInstallKiroKeepsOtherV3Hooks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".kiro/hooks/iron-brake.json"),
		`{"version":"v1","hooks":[{"name":"outro","trigger":"PreToolUse","matcher":"x","action":{"type":"command","command":"y"},"enabled":true}]}`)
	if _, err := InstallKiro(dir, "/opt/iron"); err != nil {
		t.Fatal(err)
	}
	list := readJSON(t, filepath.Join(dir, ".kiro/hooks/iron-brake.json"))["hooks"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["name"] != "outro" {
		t.Errorf("deveria acrescentar sem apagar: %v", list)
	}
}

func TestInstallKiroBrokenFileStopsAndNamesIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".kiro/agents/ruim.json"), `{"name": `)
	_, err := InstallKiro(dir, "/opt/iron")
	if err == nil || !strings.Contains(err.Error(), "ruim.json") {
		t.Errorf("deveria falhar citando o arquivo, obtive %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".kiro/hooks/iron-brake.json")); statErr == nil {
		t.Error("não deveria gravar nada depois do erro")
	}
}

func TestInstallKiroResultPaths(t *testing.T) {
	dir := t.TempDir()
	res, _ := InstallKiro(dir, "/opt/iron")
	want := []string{".kiro/agents/kiro_default.json", ".kiro/hooks/iron-brake.json"}
	got := append([]string(nil), res.Changed...)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("caminhos %v, quero %v", got, want)
	}
}

func TestFindKiroRoundTrip(t *testing.T) {
	dir := t.TempDir()
	exe := "/Users/o'neil/bin com espaço/iron"
	if _, err := InstallKiro(dir, exe); err != nil {
		t.Fatal(err)
	}
	found, err := FindKiro(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.V2) != 1 || len(found.V3) != 1 || found.V2[0].Exe != exe || found.V3[0].Exe != exe {
		t.Fatalf("deveria achar o programa com aspas e espaço: %+v", found)
	}
	if found.V2[0].Timeout != 600 || found.V3[0].Timeout != 600 {
		t.Errorf("prazos: %+v", found)
	}
}

func TestParseKiroCommandRejectsOthers(t *testing.T) {
	for _, cmd := range []string{
		`'/bin/sh' hook --agent=kiro`,         // não se chama iron
		`/opt/iron hook --agent=kiro`,         // sem aspas
		`'/opt/iron' hook --agent=claude`,     // outro agente
		`'/opt/iron' hook --agent=kiro; rm x`, // lixo depois
		``,
	} {
		if exe, ok := parseKiroCommand(cmd); ok {
			t.Errorf("%q não deveria ser reconhecido (deu %q)", cmd, exe)
		}
	}
}
