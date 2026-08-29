package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeNotesFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The notes file only grows, and a hook before every edit reads it twice — once
// for the promoted decision, once for the lifecycle states. What one read found
// answers for the next, the way the manifest is remembered (#2497).
func TestTheNotesFileIsParsedOncePerProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	writeNotesFile(t, path, `{"kind":"promoted","session":"claude:a","project":"app","state":"accepted","title":"t","text":"keep the pool","ts":"`+now+`"}`+"\n")

	before := notesParses()
	if got := LoadPromotedNotes(); len(got) != 1 || got[0].Text != "keep the pool" {
		t.Fatalf("first read is wrong: %+v", got)
	}
	if got := PromotedLifecycles(); got["claude:a"].State != "accepted" {
		t.Fatalf("lifecycles are wrong: %+v", got)
	}
	// Two derivations of the same file — the notes and the lifecycle states —
	// each parsed once. They keep different tie rules, so one cannot be built
	// from the other; what the memo removes is the repeat.
	if n := notesParses() - before; n != 2 {
		t.Errorf("the first pair of reads parsed the file %d times, want 2", n)
	}
	for i := 0; i < 3; i++ {
		LoadPromotedNotes()
		PromotedLifecycles()
	}
	if n := notesParses() - before; n != 2 {
		t.Errorf("repeat reads of an unchanged file parsed it again: %d parses in all", n)
	}

	// A file that changed is read again: the MCP server lives across many
	// calls, and a promotion made in one of them has to be visible in the next.
	writeNotesFile(t, path, `{"kind":"promoted","session":"claude:a","project":"app","state":"rejected","title":"t","text":"keep the pool","ts":"`+now+`"}`+"\n")
	if got := LoadPromotedNotes(); len(got) != 1 || got[0].State != "rejected" {
		t.Fatalf("a changed file was answered from the memo: %+v", got)
	}
}
