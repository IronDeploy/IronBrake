package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 10, 14, 0, 0, 0, time.UTC)

func newLog(t *testing.T, n int) Log {
	t.Helper()
	log := Log{Path: filepath.Join(t.TempDir(), ".iron", "audit.log")}
	for i := range n {
		entry := Entry{
			Time:     t0.Add(time.Duration(i) * time.Minute),
			Session:  "sessao-1",
			Class:    "kubectl delete",
			Decision: "deny",
			Rule:     "kubectl-delete",
		}
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	return log
}

func lines(t *testing.T, log Log) []string {
	t.Helper()
	data, err := os.ReadFile(log.Path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func writeLines(t *testing.T, log Log, ls []string) {
	t.Helper()
	if err := os.WriteFile(log.Path, []byte(strings.Join(ls, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hashOf(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])
}

func TestAppendChainsLines(t *testing.T) {
	ls := lines(t, newLog(t, 3))

	if len(ls) != 3 {
		t.Fatalf("esperava 3 linhas, obtive %d", len(ls))
	}
	for i, line := range ls {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("linha %d não é JSON: %v", i+1, err)
		}
		want := ""
		if i > 0 {
			want = hashOf(ls[i-1])
		}
		if e.Prev != want {
			t.Errorf("linha %d: prev esperava %q, obtive %q", i+1, want, e.Prev)
		}
	}
}

func TestLogIsPrivate(t *testing.T) {
	log := newLog(t, 1)

	for path, want := range map[string]os.FileMode{log.Path: 0o600, filepath.Dir(log.Path): 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != want {
			t.Errorf("%s: esperava %o, obtive %o", filepath.Base(path), want, perm)
		}
	}
}

func TestVerifyIntact(t *testing.T) {
	result := newLog(t, 5).Verify()

	if !result.OK || result.Lines != 5 {
		t.Errorf("esperava íntegro com 5 linhas, obtive %+v", result)
	}
}

func TestVerifyEmptyFile(t *testing.T) {
	log := Log{Path: filepath.Join(t.TempDir(), "audit.log")}
	if err := os.WriteFile(log.Path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	result := log.Verify()
	if !result.OK || result.Lines != 0 {
		t.Errorf("arquivo vazio: esperava íntegro com 0 linhas, obtive %+v", result)
	}
}

func TestVerifyMissingFile(t *testing.T) {
	result := Log{Path: filepath.Join(t.TempDir(), "nao-existe.log")}.Verify()

	if !result.OK || result.Lines != 0 || !result.Missing {
		t.Errorf("sem arquivo: esperava íntegro, 0 linhas e Missing, obtive %+v", result)
	}
}

func TestVerifyAlteredLine(t *testing.T) {
	log := newLog(t, 5)
	ls := lines(t, log)
	ls[2] = strings.Replace(ls[2], `"decision":"deny"`, `"decision":"allow"`, 1)
	writeLines(t, log, ls)

	result := log.Verify()
	if result.OK || result.BrokenAt != 4 {
		t.Errorf("esperava quebra na linha 4, obtive %+v", result)
	}
}

func TestVerifyRemovedLine(t *testing.T) {
	log := newLog(t, 5)
	ls := lines(t, log)
	writeLines(t, log, append(ls[:2:2], ls[3:]...))

	result := log.Verify()
	if result.OK || result.BrokenAt != 3 {
		t.Errorf("esperava quebra na linha 3, obtive %+v", result)
	}
}

func TestVerifyRemovedFirstLine(t *testing.T) {
	log := newLog(t, 3)
	writeLines(t, log, lines(t, log)[1:])

	result := log.Verify()
	if result.OK || result.BrokenAt != 1 {
		t.Errorf("esperava quebra na linha 1, obtive %+v", result)
	}
}

func TestVerifyInvalidJSON(t *testing.T) {
	log := newLog(t, 3)
	ls := lines(t, log)
	ls[1] = "isto não é JSON"
	writeLines(t, log, ls)

	result := log.Verify()
	if result.OK || result.BrokenAt != 2 {
		t.Errorf("esperava quebra na linha 2, obtive %+v", result)
	}
}

// Limitação conhecida: cortar o fim do log não é detectado.
func TestVerifyCannotDetectTruncatedEnd(t *testing.T) {
	log := newLog(t, 5)
	writeLines(t, log, lines(t, log)[:3])

	if result := log.Verify(); !result.OK {
		t.Errorf("a cadeia não tem como detectar corte no fim; obtive %+v", result)
	}
}

// Limitação conhecida: quem recalcula a corrente não é detectado.
func TestVerifyCannotDetectRewrittenChain(t *testing.T) {
	log := newLog(t, 4)
	ls := lines(t, log)

	ls[1] = strings.Replace(ls[1], `"decision":"deny"`, `"decision":"allow"`, 1)
	for i := 2; i < len(ls); i++ {
		var e Entry
		json.Unmarshal([]byte(ls[i]), &e)
		e.Prev = hashOf(ls[i-1])
		data, _ := json.Marshal(e)
		ls[i] = string(data)
	}
	writeLines(t, log, ls)

	if result := log.Verify(); !result.OK {
		t.Errorf("com a cadeia recalculada, a verificação não tem como perceber; obtive %+v", result)
	}
}

func TestConcurrentAppendsKeepChain(t *testing.T) {
	log := Log{Path: filepath.Join(t.TempDir(), "audit.log")}
	const hooks = 20

	var wg sync.WaitGroup
	for i := range hooks {
		wg.Go(func() {
			if err := log.Append(Entry{Time: t0, Session: "s", Class: "git status", Decision: "allow"}); err != nil {
				t.Errorf("hook %d: %v", i, err)
			}
		})
	}
	wg.Wait()

	if result := log.Verify(); !result.OK || result.Lines != hooks {
		t.Errorf("esperava cadeia íntegra com %d linhas, obtive %+v", hooks, result)
	}
}

func TestAppendAfterVeryLongLine(t *testing.T) {
	log := newLog(t, 1)
	var many []Resource
	for range 3000 { // ~150 KB numa linha só
		many = append(many, Resource{Address: "aws_instance.web[" + strings.Repeat("9", 20) + "]", Action: "delete"})
	}
	if err := log.Append(Entry{Time: t0, Class: "terraform apply", Decision: "ask", Resources: many}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Entry{Time: t0, Class: "git status", Decision: "allow"}); err != nil {
		t.Fatal(err)
	}

	if result := log.Verify(); !result.OK || result.Lines != 3 {
		t.Errorf("esperava cadeia íntegra com 3 linhas, obtive %+v", result)
	}
}
