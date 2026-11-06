package index

import (
	"path/filepath"
	"runtime"
	"testing"
)

// deja narrates its own builds line by line and said nothing while waiting for
// someone else's, so a command that landed mid-rebuild sat silent for the
// length of it and read as hung (#994).
func TestWaitingForAnotherBuildIsReportedOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Byte-range locks are per handle there and the second lockDir in one
		// process is a different case than two processes; the notice itself is
		// platform-independent.
		t.Skip("two locks in one process do not model contention on windows")
	}
	dir := filepath.Join(t.TempDir(), "index.db")
	notices, ends := 0, 0
	old, oldDone := LockWaitNotice, LockWaitDone
	var onNotice func()
	LockWaitNotice = func() {
		notices++
		if onNotice != nil {
			onNotice()
		}
	}
	LockWaitDone = func() { ends++ }
	t.Cleanup(func() { LockWaitNotice, LockWaitDone = old, oldDone; lockWaitNoted = false })

	// A free lock is the ordinary case and must stay quiet.
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if notices != 0 {
		t.Fatalf("an uncontended lock reported a wait: %d", notices)
	}

	// Held by someone else: the notice fires before the wait, and releasing
	// inside it proves the blocking LOCK_EX acquires immediately after.
	onNotice = unlock
	second, err := lockDir(dir)
	onNotice = nil
	if err != nil {
		t.Fatal(err)
	}
	second()
	if notices != 1 {
		t.Errorf("the wait was not reported exactly once: %d", notices)
	}
	// Its end is reported too, so a moving notice can stop.
	if ends != 1 {
		t.Errorf("the end of the wait was reported %d times", ends)
	}

	// A command takes several locks; the reader is told once, not per lock.
	lockWaitNoted = true
	noteLockWait()()
	if notices != 1 || ends != 1 {
		t.Errorf("the notice repeated itself: %d %d", notices, ends)
	}
}
