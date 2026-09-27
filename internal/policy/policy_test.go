package policy

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writePolicy(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDefault(t *testing.T) {
	p := Default()

	if want := []string{"prod", "production", "prd"}; !slices.Equal(p.ProductionPatterns, want) {
		t.Errorf("production_patterns: esperava %q, obtive %q", want, p.ProductionPatterns)
	}
	want := []string{"aws_db_instance", "aws_rds_cluster", "aws_s3_bucket", "aws_eks_cluster"}
	if !slices.Equal(p.CriticalResourceTypes, want) {
		t.Errorf("critical_resource_types: esperava %q, obtive %q", want, p.CriticalResourceTypes)
	}
}

func TestLoadWithoutFileUsesDefault(t *testing.T) {
	p, err := Load(t.TempDir())

	if err != nil {
		t.Fatalf("sem arquivo não é erro: %v", err)
	}
	if !slices.Equal(p.ProductionPatterns, Default().ProductionPatterns) {
		t.Errorf("esperava a política padrão, obtive %+v", p)
	}
}

func TestLoadEmptyFileUsesDefault(t *testing.T) {
	p, err := Load(writePolicy(t, "# só um comentário\n"))

	if err != nil {
		t.Fatalf("arquivo vazio não é erro: %v", err)
	}
	if !slices.Equal(p.CriticalResourceTypes, Default().CriticalResourceTypes) {
		t.Errorf("esperava a política padrão, obtive %+v", p)
	}
}

func TestLoadAddsToDefault(t *testing.T) {
	dir := writePolicy(t, `
production_patterns:
  - live
critical_resource_types:
  - google_sql_database_instance
`)

	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, text := range []string{"live-cluster", "prod-eu"} {
		if !p.IsProduction(text) {
			t.Errorf("%q deveria ser produção", text)
		}
	}
	for _, kind := range []string{"google_sql_database_instance", "aws_db_instance"} {
		if !p.IsCritical(kind) {
			t.Errorf("%q deveria ser crítico", kind)
		}
	}
}

func TestLoadCannotRemoveDefaults(t *testing.T) {
	p, err := Load(writePolicy(t, "production_patterns: []\ncritical_resource_types: []\n"))
	if err != nil {
		t.Fatal(err)
	}

	if !p.IsProduction("prod") || !p.IsCritical("aws_db_instance") {
		t.Errorf("os padrões embutidos sumiram: %+v", p)
	}
}

func TestLoadInvalidFileIsError(t *testing.T) {
	cases := map[string]string{
		"YAML quebrado":               "production_patterns: [prod\n",
		"chave com erro de digitação": "production_pattern:\n  - live\n",
		"lista virou texto":           "production_patterns: live\n",
		"padrão com símbolo":          "production_patterns:\n  - prod-eu\n",
		"padrão vazio":                "production_patterns:\n  - \"\"\n",
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writePolicy(t, content)); err == nil {
				t.Error("esperava erro, obtive nil")
			}
		})
	}
}

func TestLoadUnreadableFileIsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(Path(dir), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil {
		t.Error("esperava erro, obtive nil")
	}
}

func TestIsProduction(t *testing.T) {
	p := Default()
	cases := []struct {
		text string
		want bool
	}{
		{"prod", true},
		{"prod-eu", true},
		{"eks_prd1", true},
		{"PRODUCTION", true},
		{"/srv/app/production/infra", true},
		{"arn:aws:eks:us-east-1:123:cluster/prd", true},

		{"product-api", false}, // "product" não é "prod"
		{"reproduce", false},
		{"staging", false},
		{"dev-prodtest", false},
		{"", false},
	}

	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			if got := p.IsProduction(c.text); got != c.want {
				t.Errorf("esperava %v, obtive %v", c.want, got)
			}
		})
	}
}
