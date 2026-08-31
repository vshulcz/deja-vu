package index

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// heldOpen is the refusal Windows gives while a handle inside the directory is
// still open, so a test can stand where Windows is without needing it.
func heldOpen(from, to string) error {
	return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.Errno(32)}
}

// onWindows makes the wait apply on the machine running the test.
func onWindows(t *testing.T) {
	t.Helper()
	was := renameOS
	renameOS = "windows"
	t.Cleanup(func() { renameOS = was })
}

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

	onWindows(t)
	var refusals atomic.Int32
	refusals.Store(3)
	real := renameFile
	renameFile = func(from, to string) error {
		if refusals.Add(-1) >= 0 {
			return heldOpen(from, to)
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
	onWindows(t)
	var held atomic.Int32
	held.Store(2)
	real := renameFile
	renameFile = func(from, to string) error {
		// The second rename is refused for good; the restore is refused twice
		// and then allowed, which is the shape a swap meets when the same
		// handles are holding both.
		switch filepath.Base(from) {
		case "index.db.tmp":
			return heldOpen(from, to)
		case "index.db.old":
			if held.Add(-1) >= 0 {
				return heldOpen(from, to)
			}
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameFile = real })

	start := time.Now()
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("a rename refused for the whole wait was reported as a swap")
	}
	// Bounded by what a reader will wait for it, not by "eventually": a
	// raised ceiling has to be visible here.
	if took := time.Since(start); took > 2*swapRenameWait {
		t.Errorf("the swap waited %v, past the %v ceiling", took, swapRenameWait)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "old" {
		t.Errorf("the previous index is not where it was: %q %v", b, err)
	}
}

// The second rename is the one that matters to a reader: between it and the
// parking step the index is not where readers look, so a swap that waits there
// has to be done inside the window a reader waits for it (swapWindowTries ×
// swapWindowWait). This is that path — refused, then allowed.
func TestASwapWaitsOutTheSecondRenameWithinTheReadersWindow(t *testing.T) {
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
	onWindows(t)
	var refusals atomic.Int32
	refusals.Store(3)
	real := renameFile
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "index.db.tmp" && refusals.Add(-1) >= 0 {
			return heldOpen(from, to)
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameFile = real })

	start := time.Now()
	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatalf("the swap gave up on the rename readers were holding: %v", err)
	}
	if took := time.Since(start); took > swapRenameWait {
		t.Errorf("the index was away for %v, past the %v a reader waits", took, swapRenameWait)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "new" {
		t.Errorf("the new index is not in place: %q %v", b, err)
	}
}

// A refusal that will not clear — a read-only filesystem, a path that is a
// file — is reported at once rather than waited out: the reader has to act on
// it either way.
func TestASwapDoesNotWaitOutARefusalThatWillNotClear(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	onWindows(t)
	real := renameFile
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "index.db.tmp" {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EROFS}
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameFile = real })

	start := time.Now()
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("a read-only filesystem was reported as a swap")
	}
	if took := time.Since(start); took > swapRenameStep*2 {
		t.Errorf("waited %v on a refusal that cannot clear", took)
	}
}

// And a refusal that is not an errno at all — whatever a filesystem driver or
// a test double hands back — is reported rather than waited out.
func TestASwapDoesNotWaitOutAnUnrecognisedRefusal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	tmp := dir + ".tmp"
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	onWindows(t)
	real := renameFile
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "index.db.tmp" {
			return errors.New("the volume was dismounted")
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameFile = real })

	start := time.Now()
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("an unrecognised refusal was reported as a swap")
	}
	if took := time.Since(start); took > swapRenameStep*2 {
		t.Errorf("waited %v on a refusal deja cannot read", took)
	}
}
