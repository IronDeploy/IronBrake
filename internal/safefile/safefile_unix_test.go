//go:build unix

package safefile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadRejectsDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero")
	if err := os.Symlink("/dev/zero", path); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(path, 10); err == nil {
		t.Error("esperava erro para dispositivo")
	}
}

func TestReadDoesNotBlockOnFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Read(path, 10)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("esperava erro para FIFO")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read travou num FIFO")
	}
}
