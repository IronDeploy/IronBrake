//go:build unix

package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installTool(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return path
}

func TestCheckTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	noConfig := filepath.Join(t.TempDir(), "config.yaml")

	t.Run("nenhum instalado: ok", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if r := CheckTools(noConfig, project); !r.OK || !strings.Contains(r.Detail, "nenhum instalado") {
			t.Errorf("%+v", r)
		}
	})

	t.Run("no PATH e confiável: ok e mostra o caminho", func(t *testing.T) {
		path := installTool(t, "terraform", 0o755)
		r := CheckTools(noConfig, project)
		if !r.OK || !strings.Contains(r.Detail, "terraform:") || !strings.Contains(r.Detail, "(PATH)") {
			t.Errorf("%+v (%s)", r, path)
		}
	})

	t.Run("gravável por todos: falha e diz como corrigir", func(t *testing.T) {
		installTool(t, "terraform", 0o777)
		r := CheckTools(noConfig, project)
		if r.OK || !strings.Contains(r.Detail, "qualquer usuário") || !strings.Contains(r.Fix, "tools.<programa>") {
			t.Errorf("%+v", r)
		}
	})

	t.Run("dentro do projeto: falha", func(t *testing.T) {
		installTool(t, "terraform", 0o755)
		r := CheckTools(noConfig, filepath.Dir(os.Getenv("PATH")))
		if r.OK || !strings.Contains(r.Detail, "pasta do projeto") {
			t.Errorf("%+v", r)
		}
	})

	t.Run("caminho configurado vale e aparece como config.yaml", func(t *testing.T) {
		good := installTool(t, "terraform", 0o755)
		t.Setenv("PATH", t.TempDir())
		cfg := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(cfg, []byte("tools:\n  terraform: "+good+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if r := CheckTools(cfg, project); !r.OK || !strings.Contains(r.Detail, "(config.yaml)") {
			t.Errorf("%+v", r)
		}
	})

	t.Run("config.yaml com erro: falha", func(t *testing.T) {
		cfg := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(cfg, []byte("tools:\n  terraform: relativo\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if r := CheckTools(cfg, project); r.OK || !strings.Contains(r.Detail, "absoluto") || r.Fix == "" {
			t.Errorf("%+v", r)
		}
	})
}
