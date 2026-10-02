// Package fakesh cria programas falsos para os testes que precisam executar
// um "iron" de mentira (o hook que o iron doctor roda de verdade).
//
// No Unix o programa é o próprio script /bin/sh. O Windows não executa
// scripts, então ali o programa é uma cópia do binário de teste que, ao
// iniciar (MaybeRun, chamado no TestMain), reconhece o arquivo ao lado e
// interpreta o script. Só entende o que os testes usam: echo, echo ... >&2,
// touch, exec sleep e exit.
package fakesh

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sidecarExt = ".fakesh"

// Install grava o programa em path e devolve o caminho que deve ser executado
// (no Windows, path + ".exe").
func Install(t testing.TB, path, script string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path += ".exe"
	_ = os.Remove(path)
	if err := os.Link(exe, path); err != nil {
		if err := copyFile(exe, path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path+sidecarExt, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// MaybeRun deve ser a primeira linha do TestMain. Se este processo é um
// programa criado por Install, interpreta o script e encerra; senão volta.
func MaybeRun() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	script, err := os.ReadFile(exe + sidecarExt)
	if err != nil {
		return
	}
	os.Exit(interpret(string(script)))
}

func interpret(script string) int {
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "exit "):
			n, _ := strconv.Atoi(strings.TrimPrefix(line, "exit "))
			return n
		case strings.HasPrefix(line, "touch "):
			if f, err := os.Create(strings.TrimPrefix(line, "touch ")); err == nil {
				f.Close()
			}
		case strings.HasPrefix(line, "exec sleep "):
			n, _ := strconv.Atoi(strings.TrimPrefix(line, "exec sleep "))
			time.Sleep(time.Duration(n) * time.Second)
		case strings.HasPrefix(line, "echo "):
			text := strings.TrimPrefix(line, "echo ")
			out := os.Stdout
			if rest, ok := strings.CutSuffix(text, " >&2"); ok {
				text, out = rest, os.Stderr
			}
			fmt.Fprintln(out, strings.Trim(text, "'"))
		}
	}
	return 0
}
