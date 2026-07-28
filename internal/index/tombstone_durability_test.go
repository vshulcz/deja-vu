package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Forgetting is what people use on things they specifically do not want kept,
// and the record of it lived in exactly one file. Lose ~/.config — a wiped
// directory, a migration, a changed XDG_CONFIG_HOME — and the next rebuild
// brings those sessions back from source history that is still on disk.
func TestTombstonesSurviveLosingTheConfigDirectory(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	idx := filepath.Join(home, "idx")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("DEJA_INDEX_DIR", idx)
	if err := os.MkdirAll(idx, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstones(map[string]bool{"claude:sensitive-1": true}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(tombstoneMirrorPath("")); err != nil {
		t.Fatalf("no copy beside the index: %v", err)
	}
	if !readTombstones()["claude:sensitive-1"] {
		t.Fatal("tombstone not readable at all")
	}

	// The config directory is gone. What was forgotten must stay forgotten.
	if err := os.RemoveAll(cfg); err != nil {
		t.Fatal(err)
	}
	if !readTombstones()["claude:sensitive-1"] {
		t.Fatal("losing the config directory resurrected a forgotten session")
	}

	// And the other way round: the index can be rebuilt from scratch at any
	// time, so the config copy has to carry it alone too.
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstones(map[string]bool{"claude:sensitive-1": true}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(idx); err != nil {
		t.Fatal(err)
	}
	if !readTombstones()["claude:sensitive-1"] {
		t.Fatal("losing the index directory resurrected a forgotten session")
	}
}

// Unforget is the deliberate reversal and must clear both copies, or the
// session comes back on one rebuild and vanishes on the next.
func TestUnforgetClearsBothCopies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
	if err := os.MkdirAll(filepath.Join(home, "idx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstones(map[string]bool{"a": true, "b": true}); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstones(map[string]bool{"b": true}); err != nil {
		t.Fatal(err)
	}
	got := readTombstones()
	if got["a"] {
		t.Fatal("a removed tombstone survived in the mirror")
	}
	if !got["b"] {
		t.Fatal("the remaining tombstone was lost")
	}
}
