package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "windows" {
		t.Skip("o Windows não tem os bits de permissão do Unix (0600/0700)")
	}
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

// rotatingLog é um log que rotaciona a cada poucas linhas.
func rotatingLog(t *testing.T, n int) Log {
	t.Helper()
	log := Log{Path: filepath.Join(t.TempDir(), ".iron", "audit.log"), MaxSize: 600, Keep: 2}
	for i := range n {
		entry := Entry{Time: t0.Add(time.Duration(i) * time.Minute), Session: "s", Class: "kubectl delete", Decision: "deny", Rule: "kubectl-delete"}
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	return log
}

func TestRotationKeepsChainAcrossFiles(t *testing.T) {
	log := rotatingLog(t, 6) // ~4 linhas por arquivo: ainda cabe tudo em Path, Path.1 e Path.2
	if _, err := os.Stat(log.rotated(1)); err != nil {
		t.Fatalf("esperava %s depois de passar do tamanho: %v", log.rotated(1), err)
	}

	r := log.Verify()
	if !r.OK || r.Lines != 6 || r.Files < 2 {
		t.Errorf("corrente entre arquivos: %+v", r)
	}

	// A primeira linha do arquivo novo aponta para a última do rotacionado.
	old, err := os.ReadFile(log.rotated(1))
	if err != nil {
		t.Fatal(err)
	}
	oldLines := strings.Split(strings.TrimSuffix(string(old), "\n"), "\n")
	var first Entry
	if err := json.Unmarshal([]byte(lines(t, log)[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Prev != hashOf(oldLines[len(oldLines)-1]) {
		t.Error("a primeira linha do arquivo novo deveria apontar para a última do arquivo rotacionado")
	}
}

func TestRotationLimitsDiskAndKeepsChainVerifiable(t *testing.T) {
	log := rotatingLog(t, 60) // muito mais que Keep+1 arquivos

	matches, err := filepath.Glob(log.Path + ".*")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if strings.HasSuffix(m, ".lock") || strings.HasSuffix(m, ".anchor") {
			continue
		}
		if m != log.rotated(1) && m != log.rotated(2) {
			t.Errorf("arquivo além de Keep: %s", m)
		}
	}
	if _, err := os.Stat(log.anchorPath()); err != nil {
		t.Fatalf("depois de apagar os antigos, a âncora deveria existir: %v", err)
	}

	r := log.Verify()
	if !r.OK || r.Lines == 0 || r.Lines >= 60 {
		t.Errorf("esperava corrente íntegra só com o que sobrou (menos que 60 linhas): %+v", r)
	}
	if r.Bytes > 3*(600+300) {
		t.Errorf("o log passou do limite: %d bytes", r.Bytes)
	}
}

func TestRotationDetectsTamperingAcrossFiles(t *testing.T) {
	log := rotatingLog(t, 60)

	data, err := os.ReadFile(log.rotated(2))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log.rotated(2), []byte(strings.Replace(string(data), `"deny"`, `"allow"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := log.Verify(); r.OK || r.File == "" {
		t.Errorf("esperava corrente quebrada apontando o arquivo: %+v", r)
	}
}

func TestRotationDetectsMissingMiddleFile(t *testing.T) {
	log := rotatingLog(t, 60)
	if err := os.Remove(log.rotated(1)); err != nil {
		t.Fatal(err)
	}
	if r := log.Verify(); r.OK {
		t.Errorf("um arquivo removido do meio deveria quebrar a corrente: %+v", r)
	}
}

func TestDefaultLogDoesNotRotateEarly(t *testing.T) {
	log := newLog(t, 50)
	if _, err := os.Stat(log.rotated(1)); err == nil {
		t.Error("50 linhas não deveriam rotacionar com o limite padrão")
	}
}

func TestFailedAppendLeavesMarkerAndSuccessClearsIt(t *testing.T) {
	dir := t.TempDir()
	log := Log{Path: filepath.Join(dir, "audit.log")}

	// Um diretório no lugar do arquivo: abrir para escrita falha.
	if err := os.Mkdir(log.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Entry{Class: "x", Decision: "deny"}); err == nil {
		t.Fatal("esperava erro")
	}
	h := log.Check()
	if h.LastFailure == nil || h.LastFailure.Reason == "" {
		t.Fatalf("esperava o marcador da última falha: %+v", h)
	}
	if h.WriteErr == nil {
		t.Error("esperava erro de escrita no Check")
	}

	if err := os.Remove(log.Path); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Entry{Class: "x", Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	if h := log.Check(); h.LastFailure != nil || h.WriteErr != nil || !h.Chain.OK {
		t.Errorf("depois de uma gravação boa o marcador deveria sumir: %+v", h)
	}
}

func TestCheckDoesNotAppend(t *testing.T) {
	log := newLog(t, 2)
	before := lines(t, log)
	log.Check()
	if after := lines(t, log); len(after) != len(before) {
		t.Errorf("Check acrescentou linhas: %d → %d", len(before), len(after))
	}
}

func TestCheckReportsBrokenChain(t *testing.T) {
	log := newLog(t, 3)
	ls := lines(t, log)
	ls[0] = strings.Replace(ls[0], "deny", "allow", 1)
	writeLines(t, log, ls)
	if h := log.Check(); h.Chain.OK || h.WriteErr != nil {
		t.Errorf("esperava corrente quebrada e escrita ok: %+v", h)
	}
}

func TestFailureMarkerNeverHoldsLongText(t *testing.T) {
	log := Log{Path: filepath.Join(t.TempDir(), "audit.log")}
	log.trackFailure(errors.New(strings.Repeat("x", 5000)))
	h := log.Check()
	if h.LastFailure == nil || len(h.LastFailure.Reason) > maxFailureReason {
		t.Errorf("motivo deveria ser limitado a %d bytes: %+v", maxFailureReason, h.LastFailure)
	}
}

func TestConcurrentAppendsWithRotationKeepChain(t *testing.T) {
	log := Log{Path: filepath.Join(t.TempDir(), ".iron", "audit.log"), MaxSize: 800, Keep: 3}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				if err := log.Append(Entry{Time: t0, Session: "s", Class: "git push", Decision: "deny"}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()

	if r := log.Verify(); !r.OK || r.Lines == 0 {
		t.Errorf("a corrente deveria ficar íntegra com gravações simultâneas e rotação: %+v", r)
	}
}

func TestVerifyDoesNotDependOnKeep(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".iron")
	big := Log{Path: filepath.Join(dir, "audit.log"), MaxSize: 600, Keep: 8}
	for range 60 {
		if err := big.Append(Entry{Time: t0, Session: "s", Class: "kubectl delete", Decision: "deny", Rule: "kubectl-delete"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(big.rotatedNumbers()) < 6 {
		t.Fatalf("esperava mais de 5 arquivos rotacionados, obtive %d", len(big.rotatedNumbers()))
	}

	// Quem confere sem configuração (Keep padrão 5) vê tudo do mesmo jeito.
	plain := Log{Path: big.Path}
	if a, b := plain.Verify(), big.Verify(); !a.OK || a.Lines != b.Lines || a.Files != b.Files {
		t.Errorf("Verify mudou com a configuração: %+v vs %+v", a, b)
	}
}

func TestLowerKeepPrunesSeveralFilesAndKeepsChain(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".iron")
	path := filepath.Join(dir, "audit.log")
	entry := Entry{Time: t0, Session: "s", Class: "kubectl delete", Decision: "deny", Rule: "kubectl-delete"}

	wide := Log{Path: path, MaxSize: 600, Keep: 8}
	for range 60 {
		if err := wide.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	// Outro projeto, com Keep menor, dispara a rotação: poda tudo que passa de 2.
	narrow := Log{Path: path, MaxSize: 600, Keep: 2}
	for range 20 {
		if err := narrow.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if nums := narrow.rotatedNumbers(); len(nums) != 2 {
		t.Errorf("esperava só 2 rotacionados, obtive %v", nums)
	}
	if r := narrow.Verify(); !r.OK || r.Lines == 0 {
		t.Errorf("a corrente deveria continuar íntegra depois de podar vários: %+v", r)
	}
}

func TestEntriesSinceAcrossRotatedFiles(t *testing.T) {
	log := Log{Path: filepath.Join(t.TempDir(), "audit.log"), MaxSize: 1, Keep: 5}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		if err := log.Append(Entry{Time: base.Add(time.Duration(i) * time.Minute), Class: "c", Decision: "allow"}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := log.Entries(time.Time{})
	if err != nil || len(all) != 4 {
		t.Fatalf("todas: %d %v", len(all), err)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Time.Before(all[i-1].Time) {
			t.Errorf("fora de ordem: %v antes de %v", all[i-1].Time, all[i].Time)
		}
	}

	recent, _ := log.Entries(base.Add(2 * time.Minute))
	if len(recent) != 2 {
		t.Errorf("desde o minuto 2: esperava 2, obtive %d", len(recent))
	}

	// Linha ainda sendo gravada (sem JSON completo) é ignorada.
	f, _ := os.OpenFile(log.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"time":"2026-10-01T13`)
	f.Close()
	if got, err := log.Entries(time.Time{}); err != nil || len(got) != 4 {
		t.Errorf("linha incompleta deve ser ignorada: %d %v", len(got), err)
	}

	if got, err := (Log{Path: filepath.Join(t.TempDir(), "nada.log")}).Entries(time.Time{}); err != nil || len(got) != 0 {
		t.Errorf("sem log: %d %v", len(got), err)
	}
}

func TestEntriesIgnoresLinesWithoutTime(t *testing.T) {
	dir := t.TempDir()
	log := Log{Path: filepath.Join(dir, "audit.log")}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	// Arquivo rotacionado com decisões recentes, e o atual começando por uma linha sem horário.
	old := `{"time":"` + base.Add(time.Minute).Format(time.RFC3339Nano) + `","class":"git push","decision":"deny"}` + "\n"
	cur := `{"class":"lixo"}` + "\n" + `{"time":"` + base.Add(2*time.Minute).Format(time.RFC3339Nano) + `","class":"git status","decision":"allow"}` + "\n"
	if err := os.WriteFile(log.Path+".1", []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log.Path, []byte(cur), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := log.Entries(base)
	if err != nil || len(got) != 2 {
		t.Fatalf("a linha sem horário não pode esconder o arquivo mais antigo: %d %v", len(got), err)
	}
	if got[0].Class != "git push" || got[1].Class != "git status" {
		t.Errorf("ordem/conteúdo: %+v", got)
	}
}
