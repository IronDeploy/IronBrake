package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

var t0 = time.Date(2026, 1, 10, 14, 0, 0, 0, time.UTC)

func at(minutes float64) time.Time {
	return t0.Add(time.Duration(minutes * float64(time.Minute)))
}

func TestRepetitionAsksAtThreeAndDeniesAtSix(t *testing.T) {
	var s State
	infra := Activity{Command: "terraform apply tfplan"}
	want := []hook.Decision{hook.Allow, hook.Allow, hook.Ask, hook.Ask, hook.Ask, hook.Deny}

	for i, w := range want {
		got, reason := s.Evaluate(infra, at(float64(i)*0.5)) // uma vez a cada 30 s
		if got != w {
			t.Errorf("vez %d: esperava %q, obtive %q (%q)", i+1, w, got, reason)
		}
	}
}

func TestRepetitionOnlyCountsLastFiveMinutes(t *testing.T) {
	var s State
	infra := Activity{Command: "kubectl get pods"}

	for _, minute := range []float64{0, 3, 6, 9, 12} { // de 3 em 3 minutos
		if got, reason := s.Evaluate(infra, at(minute)); got != hook.Allow {
			t.Errorf("minuto %v: esperava allow, obtive %q (%q)", minute, got, reason)
		}
	}
}

func TestRepetitionDoesNotMixCommands(t *testing.T) {
	var s State
	for i, command := range []string{"kubectl get pods", "kubectl get svc", "kubectl get pods", "kubectl get svc"} {
		if got, _ := s.Evaluate(Activity{Command: command}, at(float64(i))); got != hook.Allow {
			t.Errorf("%q: esperava allow, obtive %q", command, got)
		}
	}
}

func TestRepetitionIgnoresEmptyCommand(t *testing.T) {
	var s State
	for i := range 10 {
		if got, _ := s.Evaluate(Activity{}, at(float64(i)*0.1)); got != hook.Allow {
			t.Fatalf("vez %d: esperava allow, obtive %q", i+1, got)
		}
	}
}

func TestMoreThanThreeAppliesAsks(t *testing.T) {
	var s State
	want := []hook.Decision{hook.Allow, hook.Allow, hook.Allow, hook.Ask, hook.Ask}

	for i, w := range want {
		apply := Activity{Applies: 1, Resources: 1}
		if got, reason := s.Evaluate(apply, at(float64(i)*10)); got != w {
			t.Errorf("apply %d: esperava %q, obtive %q (%q)", i+1, w, got, reason)
		}
	}
}

func TestMoreThanTwentyResourcesAsks(t *testing.T) {
	var s State

	if got, _ := s.Evaluate(Activity{Applies: 1, Resources: 15}, at(0)); got != hook.Allow {
		t.Errorf("15 recursos: esperava allow, obtive %q", got)
	}
	if got, _ := s.Evaluate(Activity{Applies: 1, Resources: 5}, at(10)); got != hook.Allow {
		t.Errorf("20 recursos: esperava allow, obtive %q", got)
	}
	if got, reason := s.Evaluate(Activity{Applies: 1, Resources: 1}, at(20)); got != hook.Ask {
		t.Errorf("21 recursos: esperava ask, obtive %q (%q)", got, reason)
	}
}

func TestOldEntriesArePruned(t *testing.T) {
	var s State
	for i := range 50 {
		s.Evaluate(Activity{Command: "kubectl get pods"}, at(float64(i)*3))
	}
	if len(s.Commands) > 2 {
		t.Errorf("esperava no máximo 2 entradas recentes, obtive %d", len(s.Commands))
	}
}

func TestStoreRemembersBetweenCalls(t *testing.T) {
	dir := t.TempDir()
	infra := Activity{Command: "terraform apply tfplan"}

	Store{Dir: dir}.Check("sessao-1", infra, at(0))
	Store{Dir: dir}.Check("sessao-1", infra, at(1))
	got, _ := Store{Dir: dir}.Check("sessao-1", infra, at(2))

	if got != hook.Ask {
		t.Errorf("a 3ª vez deveria ser ask, obtive %q", got)
	}
}

func TestStoreSeparatesSessions(t *testing.T) {
	dir := t.TempDir()
	infra := Activity{Command: "terraform apply tfplan"}

	Store{Dir: dir}.Check("sessao-1", infra, at(0))
	Store{Dir: dir}.Check("sessao-1", infra, at(1))
	got, _ := Store{Dir: dir}.Check("sessao-2", infra, at(2))

	if got != hook.Allow {
		t.Errorf("outra sessão começa do zero; esperava allow, obtive %q", got)
	}
}

func TestStoreNeverWritesCommand(t *testing.T) {
	dir := t.TempDir()
	command := "psql postgres://app:s3cr3t-senha@db/loja -c select 1"

	Store{Dir: dir}.Check("sessao-1", Activity{Command: command}, at(0))

	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) == 0 {
		t.Fatal("nenhum arquivo de estado foi gravado")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "s3cr3t") || strings.Contains(string(data), "psql") {
			t.Errorf("%s contém o comando: %s", filepath.Base(file), data)
		}
	}
}

func TestStoreFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o Windows não tem os bits de permissão do Unix (0600/0700)")
	}
	dir := filepath.Join(t.TempDir(), "sessions")
	Store{Dir: dir}.Check("sessao-1", Activity{Command: "kubectl get pods"}, at(0))

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("pasta: esperava 0700, obtive %o", perm)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, file := range files {
		info, _ := os.Stat(file)
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s: esperava 0600, obtive %o", filepath.Base(file), perm)
		}
	}
}

func TestStoreSessionIDCannotEscapeDir(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "sessions")

	Store{Dir: dir}.Check("../../fora", Activity{Command: "kubectl get pods"}, at(0))

	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 || entries[0].Name() != "sessions" {
		t.Errorf("algo foi gravado fora da pasta de estado: %v", entries)
	}
}

func TestStoreConcurrentChecksDoNotLoseUpdates(t *testing.T) {
	dir := t.TempDir()
	const hooks = 20

	var wg sync.WaitGroup
	for range hooks {
		wg.Go(func() {
			Store{Dir: dir}.Check("sessao-1", Activity{Applies: 1}, at(0))
		})
	}
	wg.Wait()

	s, err := Store{Dir: dir}.read("sessao-1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Applies != hooks {
		t.Errorf("esperava %d applies, obtive %d (contagens perdidas)", hooks, s.Applies)
	}
}

func TestStoreCorruptedStateAsksAndRecovers(t *testing.T) {
	dir := t.TempDir()
	infra := Activity{Command: "kubectl get pods"}
	Store{Dir: dir}.Check("sessao-1", infra, at(0))
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if err := os.WriteFile(files[0], []byte("{quebrado"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, _ := (Store{Dir: dir}).Check("sessao-1", infra, at(1)); got != hook.Ask {
		t.Errorf("com estado corrompido: esperava ask, obtive %q", got)
	}
	if got, _ := (Store{Dir: dir}).Check("sessao-1", Activity{Command: "kubectl get svc"}, at(2)); got != hook.Allow {
		t.Errorf("depois de recomeçar: esperava allow, obtive %q", got)
	}
}

func TestStoreWithoutDirAsks(t *testing.T) {
	if got, _ := (Store{}).Check("sessao-1", Activity{Command: "kubectl get pods"}, at(0)); got != hook.Ask {
		t.Errorf("esperava ask, obtive %q", got)
	}
}

func TestStoreSkipsIrrelevantActivity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")

	if got, _ := (Store{Dir: dir}).Check("sessao-1", Activity{}, at(0)); got != hook.Allow {
		t.Errorf("esperava allow, obtive %q", got)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a pasta de estado não deveria ter sido criada")
	}
}

func TestStoreCleansOldSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Now() // a limpeza compara com a data real dos arquivos

	Store{Dir: dir}.Check("antiga", Activity{Command: "kubectl get pods"}, now)
	Store{Dir: dir}.Check("recente", Activity{Command: "kubectl get pods"}, now)
	old, _ := filepath.Glob(filepath.Join(dir, sessionFile("antiga")+"*"))
	for _, file := range old {
		if err := os.Chtimes(file, now.Add(-25*time.Hour), now.Add(-25*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	Store{Dir: dir}.Check("outra", Activity{Command: "kubectl get pods"}, now)

	if left, _ := filepath.Glob(filepath.Join(dir, sessionFile("antiga")+"*")); len(left) != 0 {
		t.Errorf("a sessão antiga deveria ter sido apagada: %v", left)
	}
	if kept, _ := filepath.Glob(filepath.Join(dir, sessionFile("recente")+".json")); len(kept) != 1 {
		t.Error("a sessão recente deveria continuar")
	}
}

func BenchmarkStoreCheck(b *testing.B) {
	store := Store{Dir: b.TempDir()}
	now := t0
	for b.Loop() {
		store.Check("bench", Activity{Command: "kubectl get pods"}, now)
		now = now.Add(10 * time.Minute) // longe o bastante para não virar repetição
	}
}
