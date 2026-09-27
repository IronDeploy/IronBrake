package filelock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")

	release, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	start := time.Now()
	if _, err := Acquire(path, 50*time.Millisecond); err == nil {
		t.Fatal("a segunda trava deveria falhar enquanto a primeira existe")
	}
	if waited := time.Since(start); waited < 50*time.Millisecond {
		t.Errorf("deveria esperar o prazo antes de desistir; esperou %v", waited)
	}
}

func TestReleaseLetsNextIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")

	release, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release()

	release, err = Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("depois de soltar, a outra trava deveria conseguir: %v", err)
	}
	release()
}

func TestWaiterGetsLockWhenReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	release, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(30*time.Millisecond, release)

	start := time.Now()
	second, err := Acquire(path, 2*time.Second)
	if err != nil {
		t.Fatalf("deveria conseguir quando o primeiro soltasse: %v", err)
	}
	second()
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("esperou demais: %v", waited)
	}
}

func TestLockFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	release, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("esperava 0600, obtive %o", perm)
	}
}
