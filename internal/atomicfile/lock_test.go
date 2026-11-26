package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestTakeAdvisoryLockNonBlockingAndContention(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	release1, ok, busy := TakeAdvisoryLock(lockPath, false)
	if !ok || busy {
		t.Fatalf("first TakeAdvisoryLock(false) = ok:%v busy:%v, want ok:true busy:false", ok, busy)
	}

	release2, ok2, busy2 := TakeAdvisoryLock(lockPath, false)
	release2()
	if ok2 || !busy2 {
		release1()
		t.Fatalf("contended TakeAdvisoryLock(false) = ok:%v busy:%v, want ok:false busy:true", ok2, busy2)
	}

	// Double release must remain safe.
	release1()
	release1()

	release3, ok3, busy3 := TakeAdvisoryLock(lockPath, false)
	if !ok3 || busy3 {
		t.Fatalf("post-release TakeAdvisoryLock(false) = ok:%v busy:%v, want ok:true busy:false", ok3, busy3)
	}
	release3()
}

func TestTakeAdvisoryLockBlockingWait(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "wait.lock")

	release1, ok, busy := TakeAdvisoryLock(lockPath, false)
	if !ok || busy {
		t.Fatalf("initial lock failed: ok:%v busy:%v", ok, busy)
	}

	acquired := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		release2, ok2, busy2 := TakeAdvisoryLock(lockPath, true)
		if !ok2 || busy2 {
			t.Errorf("waiting TakeAdvisoryLock(true) = ok:%v busy:%v, want ok:true busy:false", ok2, busy2)
			return
		}
		close(acquired)
		release2()
	})

	select {
	case <-acquired:
		t.Fatal("waiting TakeAdvisoryLock(true) acquired before holder released")
	case <-time.After(40 * time.Millisecond):
	}

	release1()

	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("waiting TakeAdvisoryLock(true) timed out after holder released")
	}
	wg.Wait()
}

func TestTakeAdvisoryLockMissingParentDirDoesNotCreateDir(t *testing.T) {
	missingParent := filepath.Join(t.TempDir(), "nonexistent")
	lockPath := filepath.Join(missingParent, "install.lock")

	release, ok, busy := TakeAdvisoryLock(lockPath, false)
	release()
	if ok || busy {
		t.Fatalf("TakeAdvisoryLock on missing dir = ok:%v busy:%v, want ok:false busy:false", ok, busy)
	}
	if _, err := os.Stat(missingParent); !os.IsNotExist(err) {
		t.Fatalf("TakeAdvisoryLock unexpectedly created parent directory %s: err=%v", missingParent, err)
	}
}

func TestTakeAdvisoryLockSharedGroupPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions only")
	}
	t.Setenv("DEJA_SHARED_GROUP", "1")
	lockPath := filepath.Join(t.TempDir(), "shared.lock")

	release, ok, busy := TakeAdvisoryLock(lockPath, false)
	if !ok || busy {
		t.Fatalf("TakeAdvisoryLock = ok:%v busy:%v, want ok:true busy:false", ok, busy)
	}
	defer release()

	fi, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("Stat(%s): %v", lockPath, err)
	}
	if got := fi.Mode().Perm(); got != 0o660 {
		t.Fatalf("lock file mode = %#o, want 0o660", got)
	}
}
