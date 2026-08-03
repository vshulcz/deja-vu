package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A store on a disk that is not mounted looks exactly like a store whose files
// were deleted: the sessions leave the index and every surface then reads "no
// agent history was found on this machine" (#900).
func TestMissingTreesNamesADirectoryThatWentAwayWhole(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live", "proj")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	deleted := filepath.Join(live, "gone.jsonl")
	if err := os.WriteFile(deleted, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}

	// One file gone from a directory that is still there: an ordinary
	// removal, and nothing to say about it.
	if got := missingTrees(map[string]bool{deleted: true}); len(got) != 0 {
		t.Errorf("an ordinary deletion was reported as a missing tree: %+v", got)
	}

	// A whole tree gone: reported once, at the outermost directory that is no
	// longer there, with the count under it.
	vanished := filepath.Join(root, "volume", "claude", "proj")
	files := map[string]bool{
		filepath.Join(vanished, "a.jsonl"): true,
		filepath.Join(vanished, "b.jsonl"): true,
		filepath.Join(vanished, "c.jsonl"): true,
	}
	got := missingTrees(files)
	if len(got) != 1 {
		t.Fatalf("missing trees = %+v, want one", got)
	}
	if got[0].dir != filepath.Join(root, "volume") {
		t.Errorf("reported %q, want the outermost directory that is gone", got[0].dir)
	}
	if got[0].files != 3 {
		t.Errorf("counted %d files, want 3", got[0].files)
	}
	if s := pluralFiles(1); s != "" {
		t.Errorf("one file pluralised: %q", s)
	}
	if s := pluralFiles(3); !strings.HasPrefix(s, "s") {
		t.Errorf("three files not pluralised: %q", s)
	}
}
