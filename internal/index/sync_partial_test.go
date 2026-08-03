package index

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/query"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One unreadable file used to stop the whole directory, so a valid export
// sitting beside a truncated one never arrived — and the reader was told only
// about a stray character, with no line and no word about what did import
// (#891).
func TestImportKeepsGoingPastAFileItCannotRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	if err := os.WriteFile(filepath.Join(in, "a-broken.jsonl"),
		[]byte(rec("s1", "before the break")+"\nnot json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "b-good.jsonl"),
		[]byte(rec("s2", "the good file")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	n, err := Import(dir, in)
	if err == nil {
		t.Fatal("a file that could not be read was not reported")
	}
	if n == 0 {
		t.Error("the good file was held hostage to the broken one")
	}
	got := err.Error()
	if !strings.Contains(got, "a-broken.jsonl line 2") {
		t.Errorf("error does not say where: %q", got)
	}
	if !strings.Contains(got, "nothing was imported from 1 file") {
		t.Errorf("error does not say what was skipped: %q", got)
	}

	ss, err := Search(dir, query.Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range ss {
		for _, m := range s.Messages {
			texts = append(texts, m.Text)
		}
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "the good file") {
		t.Errorf("the good file did not arrive: %q", joined)
	}
	// All or nothing per file: the half before the bad line stays out.
	if strings.Contains(joined, "before the break") {
		t.Errorf("half of a refused file was imported: %q", joined)
	}
}

// deja's own exports never carry a record whose text strips to empty — the
// local ingest drops them (#868) — but a hand-made batch or one from an older
// build does, and they arrived as sessions: blank lines in `last`, rows in
// every counter (#896).
func TestImportDropsRecordsWithNothingInThem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch strings.Builder
	for _, tc := range []struct{ id, text string }{
		{"r1", "a real remote record"},
		{"r2", "   "},
		{"r3", ""},
		{"r4", "\n\t "},
	} {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: tc.id, Project: "p", Role: "user", Text: tc.text})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(in, "deja-sync-y.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	n, err := Import(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("imported %d records, want only the one with something in it", n)
	}
	counts := HarnessSessionCounts(dir)
	if counts["claude"] != 1 {
		t.Errorf("manifest holds %d claude sessions, want 1", counts["claude"])
	}
	ss, err := Search(dir, query.Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range ss {
		for _, m := range s.Messages {
			texts = append(texts, m.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "a real remote record" {
		t.Errorf("messages in the index = %q, want only the real one", texts)
	}
}

// Refusing a file has to take back everything it contributed, including the
// marks that say its records were already imported. Without that the records
// were out of the index and marked as in, so the retry after fixing the file
// brought nothing and they were lost for good (#897).
func TestRetryAfterAFixedFileBringsExactlyWhatWasMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	good := filepath.Join(in, "a-good.jsonl")
	broken := filepath.Join(in, "b-broken.jsonl")
	if err := os.WriteFile(good, []byte(rec("g1", "a good record")), 0o644); err != nil {
		t.Fatal(err)
	}
	body := rec("b1", "the first record of the broken file") + rec("b2", "the second one") + "not json\n"
	if err := os.WriteFile(broken, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	n, err := Import(dir, in)
	if err == nil {
		t.Fatal("the broken file was not reported")
	}
	if n != 1 {
		t.Fatalf("first import brought %d records, want the good file's one", n)
	}

	// The file is repaired and the transfer retried.
	if err := os.WriteFile(broken, []byte(rec("b1", "the first record of the broken file")+rec("b2", "the second one")), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err = Import(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("retry brought %d records, want the two that were missing", n)
	}
	if again, err := Import(dir, in); err != nil || again != 0 {
		t.Errorf("a third run brought %d records (err %v), want none", again, err)
	}
	if counts := HarnessSessionCounts(dir); counts["claude"] != 3 {
		t.Errorf("index holds %d claude sessions, want 3", counts["claude"])
	}
}
