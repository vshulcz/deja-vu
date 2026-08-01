package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A result line elides the middle of a long id, and the text a reader copies
// carries the "…" itself — which appears in no id, so neither the prefix nor
// the substring match from #707 can ever hit it (#853).
func TestFindByPrefixReadsAnElidedId(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	long := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"
	other := "b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0"
	when := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{
		"claude:" + long:  {ID: long, Harness: "claude", Project: "p", Started: when, Updated: when},
		"claude:" + other: {ID: other, Harness: "claude", Project: "p", Started: when, Updated: when},
	}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	// Exactly what `short()` prints for a 40-character id.
	elided := "a1b2c3d4e…d6e7f8a9b0"
	if n := PrefixMatches(dir, elided); n != 1 {
		t.Errorf("the id printed on screen resolved to %d sessions, want 1", n)
	}
	// A head and tail that belong to no session must still miss.
	if n := PrefixMatches(dir, "zzzz…zzzz"); n != 0 {
		t.Errorf("an elision matching nothing resolved to %d sessions", n)
	}
	// A plain prefix keeps working, and so does the substring fallback.
	if n := PrefixMatches(dir, "a1b2c3d4"); n != 1 {
		t.Errorf("plain prefix broke: %d", n)
	}
	if n := PrefixMatches(dir, "c9d0e1f2"); n != 1 {
		t.Errorf("substring fallback broke: %d", n)
	}
}
