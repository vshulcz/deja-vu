package index

import (
	"testing"
)

// The shape a decision has after it crosses a machine boundary: a session of
// its own carrying the state, since the receiving machine's notes file knows
// nothing about it (#2510).
func TestPromotedNoteMetasFindsWhatArrivedBySync(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	m := Manifest{Sessions: map[string]SessionMeta{
		"deja:imported-1": {ID: "imported-1", Harness: "deja", Project: "imported:home/app",
			OrigID: "deja-note-claude-dec", Lifecycle: "accepted", LifecycleNote: "the retry budget stays at 5"},
		"claude:imported-2": {ID: "imported-2", Harness: "claude", Project: "imported:home/app", OrigID: "dec"},
		"claude:local":      {ID: "local", Harness: "claude", Project: "home/app"},
	}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	got := PromotedNoteMetas(dir, nil)
	if len(got) != 1 || got[0].OrigID != "deja-note-claude-dec" {
		t.Fatalf("the note session is not what came back: %+v", got)
	}
	if n := PromotedNoteMetas(dir, func(p string) bool { return p == "home/app" }); len(n) != 0 {
		t.Errorf("a project the caller withholds still came back: %+v", n)
	}
	if n := PromotedNoteMetas("", nil); len(n) != 1 {
		t.Errorf("the default dir found %d, want 1", len(n))
	}
}
