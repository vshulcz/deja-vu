package index

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A container with a read-only mount, or a locked-down work machine, can still
// answer every question its index already holds. deja used to stop instead:
// `deja "query"` died on `open …/idx.lock: permission denied` while the index
// beside it was complete and readable.
func TestReadOnlyIndexStillAnswers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	writeLines(t, filepath.Join(claude, "project", "s.jsonl"),
		claudeLine("s1", "2026-02-01T00:01:00Z", "READONLY probe"))

	// Build the index somewhere writable, then take away the ability to
	// create the lock file beside it.
	locked := filepath.Join(home, "locked")
	dir := filepath.Join(locked, "idx")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The build left a lock file behind; remove it so the assertion below
	// is about what the read path does, not what the build did.
	_ = os.Remove(dir + ".lock")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	// The freshness check cannot run, and that is not a reason to refuse.
	if err := EnsureForSearch(dir, query.Options{Query: "readonly", All: true}, false, io.Discard); err != nil {
		t.Fatalf("EnsureForSearch on a read-only index: %v", err)
	}
	got, err := Search(dir, query.Options{Query: "readonly", All: true})
	if err != nil {
		t.Fatalf("search on a read-only index: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("read-only index answered with nothing")
	}

	// The lock file must not have been created, or the fallback is really a
	// write that happened to succeed.
	if _, err := os.Stat(dir + ".lock"); err == nil {
		t.Fatal("a lock file appeared in a read-only directory")
	}
}

// The fallback is for permission alone. Every other failure to take the lock
// still has to surface, or a broken index reads as an empty one.
func TestLockFailuresOtherThanPermissionStillSurface(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A path whose parent is a file, not a directory: MkdirAll fails with
	// something that is not a permission problem.
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, err := tryLockDir(filepath.Join(blocker, "idx"))
	if err == nil {
		t.Fatal("a structurally impossible lock path reported no error")
	}
	if ok {
		t.Fatal("reported holding a lock it could not take")
	}
}
