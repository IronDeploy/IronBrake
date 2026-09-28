package scan

import (
	"strings"
	"testing"
)

// Todos os valores aqui são FALSOS, só para fixtures.
const fakeSecret = "FAKE-do-not-use-000000000000"

func findingFor(findings []Finding, where string) (Finding, bool) {
	for _, f := range findings {
		if f.Where == where {
			return f, true
		}
	}
	return Finding{}, false
}

func TestScanEnvClassifies(t *testing.T) {
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     "AKIAFAKE",
		"AWS_SECRET_ACCESS_KEY": fakeSecret,
		"GITHUB_TOKEN":          "ghp_" + fakeSecret,
		"DATABASE_URL":          "postgres://u:" + fakeSecret + "@host/db",
		"MY_APP_SECRET":         fakeSecret,
		"HOME":                  "/home/ana",
		"PATH":                  "/usr/bin",
		"EDITOR":                "vim",
		"EMPTY_TOKEN":           "", // vazio não conta
	}

	got := ScanEnv(env)

	want := map[string]Severity{
		"AWS_ACCESS_KEY_ID":     Critical,
		"AWS_SECRET_ACCESS_KEY": Critical,
		"GITHUB_TOKEN":          High,
		"DATABASE_URL":          High,
		"MY_APP_SECRET":         Medium,
	}
	for where, sev := range want {
		f, ok := findingFor(got, where)
		if !ok {
			t.Errorf("esperava achar %q", where)
			continue
		}
		if f.Severity != sev {
			t.Errorf("%q: gravidade esperava %v, obtive %v", where, sev, f.Severity)
		}
	}

	// Não pode marcar o que é inofensivo.
	for _, benign := range []string{"HOME", "PATH", "EDITOR", "EMPTY_TOKEN"} {
		if _, ok := findingFor(got, benign); ok {
			t.Errorf("%q não deveria ser marcado", benign)
		}
	}
}

func TestAWSTemporaryIsLessSevere(t *testing.T) {
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     "ASIAFAKE",
		"AWS_SECRET_ACCESS_KEY": fakeSecret,
		"AWS_SESSION_TOKEN":     fakeSecret,
	}
	got := ScanEnv(env)

	f, ok := findingFor(got, "AWS_SECRET_ACCESS_KEY")
	if !ok {
		t.Fatal("esperava achar AWS_SECRET_ACCESS_KEY")
	}
	if f.Severity != Medium {
		t.Errorf("com session token, esperava médio, obtive %v", f.Severity)
	}
}

func TestScanEnvNeverLeaksValues(t *testing.T) {
	env := map[string]string{
		"AWS_SECRET_ACCESS_KEY": fakeSecret,
		"GITHUB_TOKEN":          fakeSecret,
		"DATABASE_URL":          "postgres://u:" + fakeSecret + "@host/db",
	}
	for _, f := range ScanEnv(env) {
		blob := f.Source + " " + f.Where + " " + f.Note
		if strings.Contains(blob, fakeSecret) {
			t.Errorf("o achado vazou o valor do segredo: %+v", f)
		}
	}
}

func TestSortBySeverity(t *testing.T) {
	env := map[string]string{
		"MY_APP_SECRET":         fakeSecret, // Medium
		"AWS_SECRET_ACCESS_KEY": fakeSecret, // Critical
		"GITHUB_TOKEN":          fakeSecret, // High
	}
	got := ScanEnv(env)
	if len(got) < 3 {
		t.Fatalf("esperava 3 achados, obtive %d", len(got))
	}
	// Crítico primeiro, médio por último.
	if got[0].Severity != Critical || got[len(got)-1].Severity != Medium {
		t.Errorf("ordem por gravidade errada: %v ... %v", got[0].Severity, got[len(got)-1].Severity)
	}
}
