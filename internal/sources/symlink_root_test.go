package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// A store reached through a symlink is the store: people put ~/.claude in a
// dotfiles repo, on an external disk, in a synced folder. Walking the link
// itself found nothing and every surface called the store found and empty
// (#1744).
func TestASymlinkedStoreRootIsWalked(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real", "proj")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-08-07T01:00:00Z","message":{"role":"user","content":"quibbleknot"}}` + "\n"
	if err := os.WriteFile(filepath.Join(real, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(filepath.Join(tmp, "real"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	direct := walkFiles(filepath.Join(tmp, "real"), func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(direct) != 1 {
		t.Fatalf("the fixture is wrong: the real path yielded %v", direct)
	}
	through := walkFiles(link, func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(through) != 1 {
		t.Errorf("a symlinked store root yielded %v, while its real path yielded %v", through, direct)
	}
}

// A link inside a store still is not followed — that is what keeps a FIFO from
// hanging the build and a link from walking out of the store — and a loop does
// not stop the walk.
func TestLinksInsideAStoreAreStillSkipped(t *testing.T) {
	tmp := t.TempDir()
	store := filepath.Join(tmp, "store", "proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-08-07T01:00:00Z","message":{"role":"user","content":"quibbleknot"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "outside.jsonl")
	if err := os.WriteFile(outside, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store, "linked.jsonl")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	loop := filepath.Join(tmp, "store", "loop")
	if err := os.MkdirAll(loop, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loop, filepath.Join(loop, "self")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := walkFiles(filepath.Join(tmp, "store"), func(p string) bool { return filepath.Ext(p) == ".jsonl" })
	if len(got) != 1 {
		t.Errorf("walk returned %v, want only the real file", got)
	}
}
