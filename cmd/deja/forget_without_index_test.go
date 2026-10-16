package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// On a machine whose index was never built, `deja forget --session` blamed an
// unmounted disk, exited 1 and wrote no tombstone, so the first `deja index`
// took the session in (#5057).
func TestForgetWithoutIndexRecordsTheTombstone(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the zanzibar rollout plan"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	kept := strings.ReplaceAll(strings.ReplaceAll(body, "a1", "b2"), "zanzibar", "harmless")
	if err := os.WriteFile(filepath.Join(root, "b2.jsonl"), []byte(kept), 0o644); err != nil {
		t.Fatal(err)
	}
	// Neither the index nor the directory it goes in exists yet.
	dir := filepath.Join(tmp, "cache", "deja", "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	if _, err := captureRun(t, "forget", "--project", "nowhere"); err == nil || strings.Contains(err.Error(), "unmounted") {
		t.Errorf("a selector nothing matches on a fresh machine: %v", err)
	}
	out, err := captureRun(t, "forget", "--session", "a1")
	if err != nil {
		t.Fatalf("forget on a machine with no index: %v", err)
	}
	if !strings.Contains(out, "nothing is indexed yet") || !strings.Contains(out, "claude:a1") {
		t.Errorf("forget did not say there was nothing indexed yet: %q", out)
	}
	if !index.Tombstoned("claude:a1") {
		t.Fatal("no tombstone was written")
	}
	if _, err := os.Stat(filepath.Join(dir, "tombstones")); err == nil {
		t.Error("a mirror was written into an index that does not exist")
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	indexed := index.IndexedSessionKeys(dir)
	if !indexed["claude:b2"] {
		t.Fatalf("the build did not index the session left alone: %v", indexed)
	}
	if indexed["claude:a1"] {
		t.Error("the first `deja index` took in the session forgotten before it")
	}
}

// The unmount message is for a build the disk went away from, not for a read
// of an index that was never built.
func TestEnsureErrorNoIndexYetIsNotAnUnmountedDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never", "index.db")
	got := ensureError(dir, fmt.Errorf("read manifest: %w", &fs.PathError{Op: "open", Path: filepath.Join(dir, "manifest.gob"), Err: syscall.ENOENT})).Error()
	if strings.Contains(got, "unmounted") || !strings.Contains(got, "nothing is indexed yet") {
		t.Errorf("index never built: %q", got)
	}
}

// doctor read a DEJA_INDEX_DIR outside the home whose directory a build would
// create as an unmounted disk. An empty mount point still reads as one.
func TestFreshHomePathOutsideHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	busy := t.TempDir()
	if err := os.WriteFile(filepath.Join(busy, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !freshHomePath(filepath.Join(busy, "idx")) {
		t.Error("a missing directory under a writable, used one was read as unmounted")
	}
	if freshHomePath(filepath.Join(t.TempDir(), "idx")) {
		t.Error("an empty directory outside the home no longer reads as a mount point")
	}
}
