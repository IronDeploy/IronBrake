package toolpath

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installExe cria um "terraform.exe" de mentira em dir e o põe no PATH.
func installExe(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "terraform.exe")
	if err := os.WriteFile(path, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return path
}

// icacls roda o icacls; sid usa o formato *S-1-... para valer em qualquer idioma.
func icacls(t *testing.T, args ...string) {
	t.Helper()
	// Caminho absoluto: o teste troca o PATH para achar o terraform falso.
	program := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	if out, err := exec.Command(program, args...).CombinedOutput(); err != nil {
		t.Fatalf("icacls %v: %v\n%s", args, err, out)
	}
}

func wantUntrusted(t *testing.T, err error, why string) {
	t.Helper()
	var u *UntrustedError
	if !errors.As(err, &u) {
		t.Fatalf("esperava UntrustedError, obtive %v", err)
	}
	if !strings.Contains(u.Why, why) {
		t.Errorf("motivo %q, quero algo com %q", u.Why, why)
	}
}

func TestResolveWindowsTrustsPrivateFolder(t *testing.T) {
	// A pasta de teste fica no perfil do usuário: só ele, o SYSTEM e os
	// Administradores gravam ali.
	path := installExe(t, filepath.Join(t.TempDir(), "bin"))

	got, err := Resolve("terraform", Config{})
	if err != nil {
		t.Fatalf("pasta privada deveria ser confiável: %v", err)
	}
	// O Resolve devolve o caminho com os links resolvidos, e isso troca o nome
	// curto 8.3 do TEMP (C:\Users\RUNNER~1) pelo longo (C:\Users\runneradmin).
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Errorf("caminho %q, quero %q", got, want)
	}
}

func TestResolveWindowsRefusesFolderWritableByUsers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	installExe(t, dir)
	icacls(t, dir, "/grant", "*S-1-5-32-545:(CI)M") // Usuários: modificar

	_, err := Resolve("terraform", Config{})
	wantUntrusted(t, err, "a pasta é gravável por todos")
}

func TestResolveWindowsRefusesFileWritableByEveryone(t *testing.T) {
	path := installExe(t, filepath.Join(t.TempDir(), "bin"))
	icacls(t, path, "/grant", "*S-1-1-0:(W)") // Todos: gravar

	_, err := Resolve("terraform", Config{})
	wantUntrusted(t, err, "qualquer usuário pode alterar o arquivo")
}

func TestResolveWindowsRefusesAuthenticatedUsers(t *testing.T) {
	// É o que acontece com uma pasta criada na raiz do disco (C:\ferramentas).
	dir := filepath.Join(t.TempDir(), "bin")
	installExe(t, dir)
	icacls(t, dir, "/grant", "*S-1-5-11:(CI)M") // Usuários Autenticados

	_, err := Resolve("terraform", Config{})
	wantUntrusted(t, err, "a pasta é gravável por todos")
}

func TestResolveWindowsConfiguredPathMustBeExecutable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "terraform.txt")
	if err := os.WriteFile(txt, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Resolve("terraform", Config{Tools: map[string]string{"terraform": txt}})
	wantUntrusted(t, err, "não é um programa executável")

	exe := filepath.Join(dir, "terraform.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve("terraform", Config{Tools: map[string]string{"terraform": exe}}); err != nil {
		t.Errorf("um .exe numa pasta privada deveria valer: %v", err)
	}
}

func TestWritableByWideGroupsReportsMissingPath(t *testing.T) {
	if _, err := writableByWideGroups(filepath.Join(t.TempDir(), "nao-existe")); err == nil {
		t.Error("caminho inexistente deveria dar erro (e o Resolve recusa)")
	}
}
