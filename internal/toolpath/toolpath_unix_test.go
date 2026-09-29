//go:build unix

package toolpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// install cria um "terraform" de mentira em dir e o põe no PATH.
func install(t *testing.T, dir string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "terraform")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // o umask do WriteFile pode ter tirado bits
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return path
}

func real(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func untrusted(t *testing.T, err error) *UntrustedError {
	t.Helper()
	var u *UntrustedError
	if !errors.As(err, &u) {
		t.Fatalf("esperava UntrustedError, obtive %v", err)
	}
	return u
}

func TestResolveFromPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := install(t, filepath.Join(t.TempDir(), "bin"), 0o755)

	got, err := Resolve("terraform", Config{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != real(t, path) {
		t.Errorf("esperava %q, obtive %q", real(t, path), got)
	}
}

func TestResolveFollowsSymlinkAndReturnsTheRealFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := install(t, filepath.Join(t.TempDir(), "Cellar"), 0o755)
	links := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(links, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(links, "terraform")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", links)

	got, err := Resolve("terraform", Config{})
	if err != nil || got != real(t, target) {
		t.Errorf("esperava %q, obtive %q (%v)", real(t, target), got, err)
	}
}

func TestResolveRejectsBinaryInsideProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	install(t, filepath.Join(project, "bin"), 0o755) // ./bin/terraform do repositório

	_, err := Resolve("terraform", Config{}, project)
	if u := untrusted(t, err); !strings.Contains(u.Why, "pasta do projeto") {
		t.Errorf("motivo: %q", u.Why)
	}
}

func TestResolveAllowsToolsWhenProjectIsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	install(t, filepath.Join(home, "bin"), 0o755)

	if _, err := Resolve("terraform", Config{}, home); err != nil {
		t.Errorf("projeto na home não deveria barrar ~/bin: %v", err)
	}
}

func TestResolveRejectsWritableByEveryone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	install(t, filepath.Join(t.TempDir(), "bin"), 0o777)
	if u := untrusted(t, func() error { _, err := Resolve("terraform", Config{}); return err }()); !strings.Contains(u.Why, "qualquer usuário") {
		t.Errorf("arquivo: %q", u.Why)
	}

	dir := filepath.Join(t.TempDir(), "bin")
	install(t, dir, 0o755)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if u := untrusted(t, func() error { _, err := Resolve("terraform", Config{}); return err }()); !strings.Contains(u.Why, "pasta é gravável") {
		t.Errorf("pasta: %q", u.Why)
	}
}

func TestResolveRejectsNotExecutable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := Config{Tools: map[string]string{"terraform": install(t, filepath.Join(t.TempDir(), "bin"), 0o644)}}

	if u := untrusted(t, func() error { _, err := Resolve("terraform", cfg); return err }()); !strings.Contains(u.Why, "execução") {
		t.Errorf("motivo: %q", u.Why)
	}
}

func TestResolveNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Resolve("terraform", Config{})
	var u *UntrustedError
	if err == nil || errors.As(err, &u) {
		t.Errorf("programa ausente deveria ser um erro comum, obtive %v", err)
	}
}

func TestResolveConfiguredPathWinsOverPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	good := install(t, filepath.Join(t.TempDir(), "good"), 0o755)
	// O PATH tem um terraform falso (gravável por todos); o configurado vale.
	fake := filepath.Join(t.TempDir(), "fake")
	install(t, fake, 0o777)
	t.Setenv("PATH", fake)

	got, err := Resolve("terraform", Config{Tools: map[string]string{"terraform": good}})
	if err != nil || got != real(t, good) {
		t.Errorf("esperava o configurado %q, obtive %q (%v)", real(t, good), got, err)
	}
}

func TestConfiguredPathInsideProjectIsAllowed(t *testing.T) {
	// Quem configura escolhe: o config.yaml é do usuário, não do repositório.
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	path := install(t, filepath.Join(project, "tools"), 0o755)

	got, err := Resolve("terraform", Config{Tools: map[string]string{"terraform": path}}, project)
	if err != nil || got != real(t, path) {
		t.Errorf("%q (%v)", got, err)
	}
}

func TestLoad(t *testing.T) {
	write := func(t *testing.T, content string) string {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if c, err := Load(filepath.Join(t.TempDir(), "nao-existe.yaml")); err != nil || len(c.Tools) != 0 {
		t.Errorf("ausente deveria ser vazio: %+v %v", c, err)
	}
	if c, err := Load(write(t, "tools:\n  terraform: /opt/homebrew/bin/terraform\n  tofu: /usr/local/bin/tofu\n")); err != nil || c.Tools["terraform"] != "/opt/homebrew/bin/terraform" || c.Tools["tofu"] != "/usr/local/bin/tofu" {
		t.Errorf("%+v %v", c, err)
	}

	for name, content := range map[string]string{
		"caminho relativo":      "tools:\n  terraform: ./terraform\n",
		"só o nome":             "tools:\n  terraform: terraform\n",
		"programa desconhecido": "tools:\n  kubectl: /usr/bin/kubectl\n",
		"chave desconhecida":    "tool:\n  terraform: /x/terraform\n",
		"YAML quebrado":         "tools: [\n",
		"tools virou texto":     "tools: terraform\n",
	} {
		if _, err := Load(write(t, content)); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: esperava ErrConfig, obtive %v", name, err)
		}
	}
}
