//go:build unix

package policy

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLoadRejectsSpecialFiles(t *testing.T) {
	for name, create := range map[string]func(path string) error{
		"/dev/zero": func(path string) error { return os.Symlink("/dev/zero", path) },
		"FIFO":      func(path string) error { return syscall.Mkfifo(path, 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := create(Path(dir)); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() { _, err := Load(dir); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Error("esperava erro")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Load travou")
			}
		})
	}
}
