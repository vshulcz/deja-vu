package index

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Windows refuses to rename a directory while a handle inside it is open, so
// two ordinary passes at once left the loser unable to swap and the store a
// session short (#2228). The reader it is waiting on is finishing a read, so
// the swap waits it out rather than giving up on the pass.
func TestASwapWaitsOutARefusedRename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "records.bin"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	var refusals atomic.Int32
	refusals.Store(3)
	real := renameFile
	renameFile = func(from, to string) error {
		if refusals.Add(-1) >= 0 {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("Access is denied.")}
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameFile = real })

	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatalf("the swap gave up on a rename that was only being held: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "new" {
		t.Errorf("the new index is not in place: %q %v", b, err)
	}
	if _, err := os.Stat(dir + ".old"); err == nil {
		t.Error("the parking spot was left behind")
	}
}

// A rename refused for the whole wait is a failure the pass reports, with the
// previous index still where it was: leaving nothing there is worse than
// leaving what the reader already had.
func TestASwapThatCannotRenameKeepsTheOldIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	real := renameFile
	renameFile = func(from, to string) error {
		// Only the second rename is refused, so the parking step succeeds and
		// the restore has something to put back.
		if filepath.Base(from) == "index.db.tmp" {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("Access is denied.")}
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameFile = real })

	start := time.Now()
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("a rename refused for the whole wait was reported as a swap")
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the swap waited %v, which is no longer a bounded wait", took)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "old" {
		t.Errorf("the previous index is not where it was: %q %v", b, err)
	}
}
