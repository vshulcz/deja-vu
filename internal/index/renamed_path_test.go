package index

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// A rename arrives as one removed path and one added path in the same pass, so
// the path the manifest holds is the one that went away. Comparing them called
// it a collision with a file that no longer exists: the row kept the dead path
// until a full rebuild, and `forget` warned that dropping the session would
// take a second conversation with it (#1086).
func TestARenamedTranscriptIsNotASecondCopyOfItself(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")

	// b sorts after a, which is the direction that used to keep the dead path:
	// the tie-break takes the lexicographically smaller one.
	from := filepath.Join(claude, "project", "a.jsonl")
	to := filepath.Join(claude, "project", "b.jsonl")
	writeLines(t, from, claudeLine("sess-a", "2026-01-01T00:01:00Z", "widget calibration drifted"))
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:sess-a"]
	if !ok {
		t.Fatalf("the renamed session left the index: %v", m.Sessions)
	}
	if meta.Path != to {
		t.Errorf("path = %q, want the file that exists, %q", meta.Path, to)
	}
	if meta.Shared {
		t.Error("a renamed transcript was marked as sharing its id with another")
	}
}

// The tie-break still has to fire when two transcripts really do claim one id,
// even when some unrelated file went away in the same pass.
func TestTwoLiveTranscriptsSharingAnIdStillCollide(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")

	one := filepath.Join(claude, "p1", "x.jsonl")
	gone := filepath.Join(claude, "p1", "unrelated.jsonl")
	writeLines(t, one, claudeLine("dup-1", "2026-01-01T00:01:00Z", "widget calibration drifted"))
	writeLines(t, gone, claudeLine("other", "2026-01-01T00:01:00Z", "nothing to do with it"))
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	// A second live transcript claiming the same id, and a removal beside it.
	two := filepath.Join(claude, "p2", "y.jsonl")
	writeLines(t, two, claudeLine("dup-1", "2026-01-01T00:02:00Z", "widget calibration drifted again"))
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:dup-1"]
	if !ok {
		t.Fatalf("the shared session left the index: %v", m.Sessions)
	}
	if !meta.Shared {
		t.Error("two live transcripts claiming one id were not reported as sharing it")
	}
}
