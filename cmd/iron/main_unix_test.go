//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLoadEnvDoesNotBlockOnSpecialFiles(t *testing.T) {
	isolateEnv(t, "")
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".terraform"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(cwd, ".terraform", "environment"), 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { loadEnv(cwd); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loadEnv travou")
	}
}
