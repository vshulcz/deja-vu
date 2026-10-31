package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// The import path redacted a peer's text but threw the count away, so
// `stats --redaction` under-reported protection on an imported store: two
// secrets removed, the total said one. Import now tallies its redactions the
// way local ingest does, and rolls the count back with a refused file.
func TestImportCountsItsRedactions(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(id, text string) string {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: id, Project: "p", Role: "user", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	// Two records, each carrying one AWS access key.
	if err := os.WriteFile(filepath.Join(in, "deja-sync.jsonl"),
		[]byte(rec("s1", "key AKIAIOSFODNN7EXAMPLE one")+rec("s2", "key AKIAIOSFODNN7EXAMPLE two")), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	if _, err := Import(dir, in); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Redacted != 2 {
		t.Fatalf("import counted %d redactions, want 2 (one per imported secret)", m.Redacted)
	}

	// Re-importing the same batch redacts nothing new — the records dedupe
	// before they are counted, so the total must not climb.
	if _, err = Import(dir, in); err != nil {
		t.Fatal(err)
	}
	if m, err = readManifest(dir); err != nil {
		t.Fatal(err)
	}
	if m.Redacted != 2 {
		t.Fatalf("re-import double-counted redactions: total=%d, want 2", m.Redacted)
	}
}

// A file refused for truncation takes its redaction count with it: the count
// is applied only once the file has been read whole, so a secret in the half
// before the break does not inflate the total for records that never landed.
func TestImportRedactionRollsBackWithARefusedFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(id, text string) string {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: id, Project: "p", Role: "user", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// A valid record with a secret, then a truncated line — the file is refused.
	body := rec("t1", "secret AKIAIOSFODNN7EXAMPLE here") + "\n" + `{"harness":"claude","session_id":"t1","role":"user","text":"trunc`
	if err := os.WriteFile(filepath.Join(in, "deja-sync-trunc.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	if _, err := Import(dir, in); err == nil {
		t.Fatal("a truncated file was not reported")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Redacted != 0 {
		t.Fatalf("a refused file left its redaction count behind: total=%d, want 0", m.Redacted)
	}
}

// Rebuilding an index that holds imported records passes them through
// preRedactSessions again; a Cookie header followed by JSON-escaped newlines
// must not erode 383 bytes or double-count redactions on each rebuild (#5763).
func TestRebuildPreservesImportedCookieRecordAndRedactionCounts(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	text := `"headers": "Cookie: session=secret_value_123456\nContent-Type: application/json\n\n` + strings.Repeat("ordinary-body-", 35) + `"`
	b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "cookie-imp", Project: "p", Role: "user", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(in, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	if _, err = Import(dir, in); err != nil {
		t.Fatal(err)
	}
	s1 := importedSessions(dir).sessions
	if len(s1) != 1 || len(s1[0].Messages) != 1 {
		t.Fatalf("imported session missing: %#v", s1)
	}
	wantText := s1[0].Messages[0].Text

	if err = EnsureForSearch(dir, search.Options{Query: "ordinary-body-"}, true, nil); err != nil {
		t.Fatal(err)
	}
	s2 := importedSessions(dir).sessions
	if len(s2) != 1 || len(s2[0].Messages) != 1 {
		t.Fatalf("session lost on rebuild: %#v", s2)
	}
	if s2[0].Messages[0].Text != wantText {
		t.Fatalf("rebuild eroded imported record (%d -> %d bytes):\n  before: %q\n  after:  %q",
			len(wantText), len(s2[0].Messages[0].Text), wantText, s2[0].Messages[0].Text)
	}
	m2, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Redacted != 0 {
		t.Fatalf("rebuild re-redacted an already-redacted imported record: m2.Redacted=%d (%v)",
			m2.Redacted, m2.RedactionRules)
	}
}
