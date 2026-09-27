package safefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.yaml")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := Read(path, 10)
	if err != nil || string(data) != "ok" {
		t.Errorf("esperava \"ok\", obtive %q, %v", data, err)
	}
}

func TestReadMissingFileIsNotExist(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "nao-existe"), 10)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("esperava fs.ErrNotExist, obtive %v", err)
	}
}

func TestReadRejectsTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grande")
	if err := os.WriteFile(path, make([]byte, 11), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(path, 10); err == nil {
		t.Error("esperava erro para arquivo maior que o limite")
	}
}
