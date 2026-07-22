//go:build !windows

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockDir(dir string) (func(), error) {
	lockPath := dir + ".lock"
	// Tighten pre-existing indexes created before the 0700 default.
	_ = os.Chmod(dir, 0o700)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock index: %w", err)
	}
	// Under the lock: finish any swap interrupted mid-rename. Running this
	// before the flock raced a concurrent swap's missing-dir window (#181).
	recoverIndexDir(dir)
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// tryLockDir is lockDir without blocking: ok=false means another process
// (typically a detached rebuild) holds the lock. Read paths fall back to
// lock-free snapshot reads — the atomic directory swap plus the corrupt-index
// recovery retry make that safe, while waiting here would stall an MCP tool
// call for the length of a rebuild.
func tryLockDir(dir string) (func(), bool, error) {
	lockPath := dir + ".lock"
	_ = os.Chmod(dir, 0o700)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false, nil
	}
	recoverIndexDir(dir)
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
