package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Every value below is synthetic.

func importKeysEnv(t *testing.T) string {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	return tmp
}

func writeBatchFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A peer whose redaction rules widened re-sends the same record with a marker
// where the value was. It is the same record, and it lands once.
func TestReRedactedRecordImportsOnce(t *testing.T) {
	tmp := importKeysEnv(t)
	dir := filepath.Join(tmp, "index.db")
	b1 := writeBatchFile(t, filepath.Join(tmp, "b1"), "deja-sync-a.jsonl",
		`{"harness":"claude","session_id":"s-1","project":"p","role":"user","text":"the staging password is hunterhunt-2026 keep it","time":"2026-01-02T06:04:05Z","origin":"laptop"}
`)
	if _, err := Import(dir, b1); err != nil {
		t.Fatal(err)
	}
	b2 := writeBatchFile(t, filepath.Join(tmp, "b2"), "deja-sync-b.jsonl",
		`{"harness":"claude","session_id":"s-1","project":"p","role":"user","text":"the staging password is [redacted:credential] keep it","time":"2026-01-02T06:04:05Z","origin":"laptop"}
`)
	n, err := Import(dir, b2)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(recs) != 1 {
		t.Errorf("same record re-sent with re-redacted text: imported %d, session now holds %d records, want 0 and 1", n, len(recs))
	}
}

// A batch from a build that renamed `text` imports nothing, and says so: the
// records are counted, so the import does not read as an empty batch.
func TestTextlessRecordsAreCounted(t *testing.T) {
	tmp := importKeysEnv(t)
	dir := filepath.Join(tmp, "index.db")
	b := writeBatchFile(t, filepath.Join(tmp, "b"), "deja-sync-c.jsonl",
		`{"harness":"claude","session_id":"s-2","project":"p","role":"user","content":"how do we rotate the widget cache","time":"2026-01-03T06:04:05Z"}
{"harness":"claude","session_id":"s-2","project":"p","role":"assistant","content":"we rotate it on deploy","time":"2026-01-03T06:05:05Z"}
`)
	n, err := Import(dir, b)
	if n == 0 && err == nil && ImportSkippedIncomplete() == 0 && ImportSkippedForgotten() == 0 && ImportSkippedOwn() == 0 && ImportSkippedExcluded() == 0 {
		t.Errorf("2 records without text were dropped with no error and no count; the import reads as an empty batch")
	}
}
