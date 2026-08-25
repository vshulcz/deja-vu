package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A process killed between the record and its newline costs that record — the
// log is appended without a lock by design and `read` drops a line that does
// not parse. It cost one more: the next event was appended onto the partial
// line, so that line failed to parse too and the recall just served went
// missing from every count (#1901).
func TestARecordAfterATruncatedTailSurvives(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	good := `{"t":"2026-08-20T10:00:00Z","kind":"recall","bytes":100,"sessions":1}`
	partial := `{"t":"2026-08-21T10:00:00Z","kind":"recall","byte`
	if err := os.WriteFile(p, []byte(good+"\n"+partial), 0o600); err != nil {
		t.Fatal(err)
	}

	RecordResult(dir, KindRecall, 200, 1, false)

	tot := Totals(dir)
	if tot.Recalls != 2 {
		body, _ := os.ReadFile(p)
		t.Fatalf("counted %d recalls, want the old one and the new one:\n%s", tot.Recalls, body)
	}
	if tot.Bytes != 300 {
		t.Errorf("counted %d bytes, want 100 + 200", tot.Bytes)
	}
	// The partial record itself stays lost — that is the trade the log makes —
	// but it must not take a second line with it.
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"byte{`) {
		t.Errorf("the new record was appended onto the partial line:\n%s", body)
	}
}

// The ordinary case is unchanged: no stray blank line, no doubled newline.
func TestAnOrdinaryRecordAddsOneLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	RecordResult(dir, KindRecall, 100, 1, false)
	RecordResult(dir, KindRecall, 200, 1, false)
	body, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"); len(lines) != 2 {
		t.Errorf("two records wrote %d lines:\n%s", len(lines), body)
	}
	if strings.Contains(string(body), "\n\n") {
		t.Errorf("a blank line crept in:\n%s", body)
	}
}
