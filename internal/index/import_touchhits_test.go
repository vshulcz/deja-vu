package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Touched is a ranking, and #1333 established that a ranking cannot be merged
// without the numbers behind it — which is why SessionMeta.TouchHits exists and
// why local ingest carries it. The import path derives Touched from the records
// (counting as it goes) and then throws the counts away, so a peer's session
// merges by rank alone: a file the second batch touched once leads one the first
// batch touched throughout (#2558).
func TestImportKeepsTheCountsBehindTouched(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	rec := func(role, text string, n int) string {
		return `{"harness":"claude","session_id":"peer1","project":"svc","role":"` + role +
			`","text":"` + text + `","time":"` + at.Add(time.Duration(n)*time.Minute).Format(time.RFC3339) + `"}` + "\n"
	}
	batch := func(name, body string) {
		if err := os.WriteFile(filepath.Join(shared, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Import(dir, shared); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(shared, name)); err != nil {
			t.Fatal(err)
		}
	}
	// The first batch passes over one file once.
	batch("deja-sync-a.jsonl", rec("user", "a first look", 0)+rec("files", "/repo/tail.go", 1))

	meta := importedPeerMeta(t, dir)
	if len(meta.TouchHits) != len(meta.Touched) {
		t.Errorf("Touched=%v carries %d counts", meta.Touched, len(meta.TouchHits))
	}

	// The second works on another file throughout.
	second := rec("user", "working on the pool", 10)
	for i := 0; i < 5; i++ {
		second += rec("files", "/repo/pool.go", 11+i)
	}
	batch("deja-sync-b.jsonl", second)

	meta = importedPeerMeta(t, dir)
	if len(meta.Touched) < 2 {
		t.Fatalf("Touched=%v, want both files", meta.Touched)
	}
	if meta.Touched[0] != "/repo/pool.go" {
		t.Errorf("Touched=%v hits=%v — the file touched once leads the one touched throughout",
			meta.Touched, meta.TouchHits)
	}
}

func importedPeerMeta(t *testing.T, dir string) SessionMeta {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range metas {
		if meta.OrigID == "peer1" {
			return meta
		}
	}
	t.Fatalf("imported session not found in %d metas", len(metas))
	return SessionMeta{}
}
