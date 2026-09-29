//go:build unix

package tfplan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IronDeploy/IronBrake/internal/toolpath"
)

// fakeTool põe no PATH um programa que grava os argumentos e a pasta em que
// rodou, e imprime out.
func fakeTool(t *testing.T, name, out string) (record string) {
	t.Helper()
	bin := t.TempDir()
	record = filepath.Join(t.TempDir(), "chamada")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" \"$PWD\" \"$TG_NON_INTERACTIVE\" > '" + record + "'\ncat <<'EOF'\n" + out + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

func readRecord(t *testing.T, path string) (args, pwd, nonInteractive string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return lines[0], lines[1], lines[2]
}

func TestShowRunsTerragruntInModuleDir(t *testing.T) {
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "envs", "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	record := fakeTool(t, "terragrunt", `{"format_version":"1.2"}`)

	out, err := Show(Request{Tool: "terragrunt", Cwd: cwd, Chdir: "envs/dev", PlanFile: "tfplan"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "format_version") {
		t.Errorf("saída: %q", out)
	}
	args, pwd, nonInteractive := readRecord(t, record)
	if args != "show -json tfplan" || pwd != filepath.Join(cwd, "envs", "dev") || nonInteractive != "true" {
		t.Errorf("chamada: args=%q pwd=%q nonInteractive=%q", args, pwd, nonInteractive)
	}
}

func TestShowTerragruntRunAll(t *testing.T) {
	record := fakeTool(t, "terragrunt", `{"format_version":"1.2"}`)
	if _, err := Show(Request{Tool: "terragrunt", Cwd: t.TempDir(), PlanFile: "tfplan", RunAll: []string{"run", "--all", "--"}}); err != nil {
		t.Fatal(err)
	}
	if args, _, _ := readRecord(t, record); args != "run --all -- show -json tfplan" {
		t.Errorf("args: %q", args)
	}
}

func TestShowTofuAndTerraformUseChdirFlag(t *testing.T) {
	for _, tool := range []string{"tofu", "terraform"} {
		record := fakeTool(t, tool, `{"format_version":"1.2"}`)
		if _, err := Show(Request{Tool: tool, Cwd: t.TempDir(), Chdir: "infra", PlanFile: "tfplan"}); err != nil {
			t.Fatal(err)
		}
		if args, _, _ := readRecord(t, record); args != "-chdir=infra show -json tfplan" {
			t.Errorf("%s: args %q", tool, args)
		}
	}
}

func TestShowRejectsUnknownTool(t *testing.T) {
	if _, err := Show(Request{Tool: "/tmp/evil", PlanFile: "tfplan"}); err == nil {
		t.Error("esperava erro para ferramenta fora da lista")
	}
}

func TestShowDoesNotRunToolFromProjectOrConfigOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()

	// Um terraform do projeto no PATH: nem chega a executar.
	bin := filepath.Join(project, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "rodou")
	script := "#!/bin/sh\ntouch '" + marker + "'\necho '{\"format_version\":\"1.2\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "terraform"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	_, err := Show(Request{Tool: "terraform", Cwd: project, PlanFile: "tfplan", ProjectDirs: []string{project}})
	if err == nil {
		t.Fatal("esperava recusar o terraform de dentro do projeto")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("o programa suspeito foi executado")
	}

	// Com o caminho configurado (fora do projeto), roda esse e não o do PATH.
	good := fakeTool(t, "terraform", `{"format_version":"1.2"}`) // põe outro no PATH
	goodPath, _ := exec.LookPath("terraform")
	t.Setenv("PATH", bin+":/usr/bin:/bin") // o terraform do PATH volta a ser só o suspeito
	out, err := Show(Request{Tool: "terraform", Cwd: project, PlanFile: "tfplan", ProjectDirs: []string{project},
		Config: toolpath.Config{Tools: map[string]string{"terraform": goodPath}}})
	if err != nil || !strings.Contains(string(out), "format_version") {
		t.Fatalf("esperava usar o caminho configurado: %v", err)
	}
	if _, statErr := os.Stat(good); statErr != nil {
		t.Error("o programa configurado deveria ter rodado")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("o programa do PATH não deveria ter rodado")
	}
}
