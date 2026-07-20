//go:build !windows

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockDir(dir string) (func(), error) {
	recoverIndexDir(dir)
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
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
