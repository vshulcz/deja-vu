package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A day bucket's id is the sending machine's rendering of a date, not the
// identity of what it holds. After that machine changed zone the same note
// arrived under a new bucket and every peer grew a second copy (#977).
func TestANoteDoesNotArriveTwiceUnderTwoBucketIds(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, recs ...SyncRecord) {
		t.Helper()
		var batch []byte
		for _, r := range recs {
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			batch = append(batch, append(b, '\n')...)
		}
		if err := os.WriteFile(filepath.Join(exp, name), batch, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const text = "note written next morning"
	// The same note, exported before and after the sender changed zone: same
	// project, same timestamp, different bucket.
	write("deja-sync-a.jsonl", SyncRecord{Harness: "deja", SessionID: "deja-2026-07-13-p", Project: "p", Role: "user", Text: text})
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	write("deja-sync-b.jsonl", SyncRecord{Harness: "deja", SessionID: "deja-2026-07-12-p", Project: "p", Role: "user", Text: text})
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}

	ss, err := Search(dir, query.Options{Query: "next morning", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("the same note landed in %d sessions, want 1", len(ss))
	}

	// A different note in the same bucket is not a duplicate.
	write("deja-sync-c.jsonl", SyncRecord{Harness: "deja", SessionID: "deja-2026-07-12-p", Project: "p", Role: "user", Text: "a different note entirely"})
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(dir, query.Options{Query: "different note entirely", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Error("a genuinely new note was dropped as a duplicate")
	}
	// And an ordinary session still dedupes on its own id.
	write("deja-sync-d.jsonl", SyncRecord{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "an ordinary transcript line"})
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(dir, query.Options{Query: "ordinary transcript line", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("an ordinary session landed in %d sessions, want 1", len(ss))
	}
}
