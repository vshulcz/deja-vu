package index

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
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
	notices := 0
	old := LockWaitNotice
	LockWaitNotice = func() { notices++ }
	t.Cleanup(func() { LockWaitNotice = old; lockWaitNoted = false })

	// A free lock is the ordinary case and must stay quiet.
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if notices != 0 {
		t.Fatalf("an uncontended lock reported a wait: %d", notices)
	}

	// Held by someone else: the notice fires before the wait.
	done := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		unlock()
		close(done)
	}()
	second, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	second()
	if notices != 1 {
		t.Errorf("the wait was not reported exactly once: %d", notices)
	}

	// A command takes several locks; the reader is told once, not per lock.
	lockWaitNoted = true
	noteLockWait()
	if notices != 1 {
		t.Errorf("the notice repeated itself: %d", notices)
	}
}
