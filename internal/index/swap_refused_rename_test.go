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

// swapClock stands the swap on a clock that moves only when the swap sleeps,
// and returns how far it has moved. What a test bounds is the waiting the swap
// chose, not how long a loaded runner took to do it: the same swap measured
// 306ms against a 284ms bound on ubuntu-latest (#4160).
func swapClock(t *testing.T) func() time.Duration {
	t.Helper()
	start := time.Unix(0, 0)
	var waited time.Duration
	wasNow, wasSleep := swapNow, swapSleep
	swapNow = func() time.Time { return start.Add(waited) }
	swapSleep = func(d time.Duration) { waited += d }
	t.Cleanup(func() { swapNow, swapSleep = wasNow, wasSleep })
	return func() time.Duration { return waited }
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
		// Every rename costs a little on a loaded runner, which is time the
		// swap did not choose to spend (#4160).
		time.Sleep(20 * time.Millisecond)
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

	away := swapClock(t)
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("a rename refused for the whole wait was reported as a swap")
	}
	// Bounded by what a reader will wait for it, not by "eventually": the
	// failed rename and the restore share that window, with a floor under the
	// restore so it is never left none of it. Counted on the swap's own clock,
	// so neither a windows sleep rounding up to the scheduler tick (#3648) nor
	// a loaded runner (#4160) moves it.
	bound := swapRenameWait + restoreRenameFloor + swapRenameStep
	if took := away(); took > bound {
		t.Errorf("the index was away for %v, past the %v it may take", took, bound)
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

	away := swapClock(t)
	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatalf("the swap gave up on the rename readers were holding: %v", err)
	}
	if took := away(); took > swapRenameWait {
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
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
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

	away := swapClock(t)
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("a read-only filesystem was reported as a swap")
	}
	if took := away(); took > 0 {
		t.Errorf("waited %v on a refusal that cannot clear", took)
	}
	// And the previous index is back: a fail-fast is still a swap that did not
	// happen, not one that took the index with it.
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "old" {
		t.Errorf("the previous index is not where it was: %q %v", b, err)
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
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
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

	away := swapClock(t)
	if err := swapIndexDir(dir, tmp); err == nil {
		t.Fatal("an unrecognised refusal was reported as a swap")
	}
	if took := away(); took > 0 {
		t.Errorf("waited %v on a refusal deja cannot read", took)
	}
	// And the previous index is back: a fail-fast is still a swap that did not
	// happen, not one that took the index with it.
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "old" {
		t.Errorf("the previous index is not where it was: %q %v", b, err)
	}
}

// The parking step is the rename nobody is waiting on — the index is still
// where readers look until it lands — so it waits far past the window that
// bounds the pair after it. That is the case #2228 is about: a pass that spent
// seconds building the new index must not give up because a search held a
// handle for a quarter of a second.
func TestTheParkingStepWaitsPastTheReadersWindow(t *testing.T) {
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
	// Past the reader's window by a margin, and well inside the parking step's.
	refusals := int32(swapRenameWait/swapRenameStep) + 5
	var left atomic.Int32
	left.Store(refusals)
	real := renameFile
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "index.db" && left.Add(-1) >= 0 {
			return heldOpen(from, to)
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameFile = real })

	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatalf("the swap gave up on the parking step: %v", err)
	}
	if left.Load() >= 0 {
		t.Fatalf("the parking step was never held for the whole window: %d refusals left", left.Load())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "records.bin")); err != nil || string(b) != "new" {
		t.Errorf("the new index is not in place: %q %v", b, err)
	}
}

// The manifest is renamed over while readers decode it, and Windows refuses
// that rename for as long as one of them has the file open. The write gave up
// on the first refusal: a recall beside a remember failed with "Access is
// denied" on windows CI.
func TestAManifestWriteWaitsOutAReader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.gob")
	if err := writeGobAtomic(p, manifestCore{Version: 1}); err != nil {
		t.Fatal(err)
	}

	onWindows(t)
	swapClock(t)
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

	if err := writeGobAtomic(p, manifestCore{Version: 2}); err != nil {
		t.Fatalf("the write gave up on a rename a reader was only holding: %v", err)
	}
	if refusals.Load() >= 0 {
		t.Fatalf("the write never met the refusals: %d left", refusals.Load())
	}
	var got manifestCore
	if err := readGob(p, &got); err != nil || got.Version != 2 {
		t.Errorf("manifest = %+v, %v; want the new one in place", got, err)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("the temp file was left behind")
	}
}

// The same on a real handle, which only Windows refuses: the reader closes
// while the writer is waiting, and the write lands.
func TestAManifestWriteLandsAfterARealReaderCloses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.gob")
	if err := writeGobAtomic(p, manifestCore{Version: 1}); err != nil {
		t.Fatal(err)
	}
	r, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- writeGobAtomic(p, manifestCore{Version: 2}) }()
	time.Sleep(50 * time.Millisecond)
	_ = r.Close()
	if err := <-done; err != nil {
		t.Fatalf("the write failed behind a reader that closed: %v", err)
	}
	var got manifestCore
	if err := readGob(p, &got); err != nil || got.Version != 2 {
		t.Errorf("manifest = %+v, %v; want the new one in place", got, err)
	}
}
