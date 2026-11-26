//go:build !windows

package main

import (
	"errors"
	"os"
	"sync"
	"syscall"
)

var mcpUmaskMu sync.Mutex

// withPrivateUmask temporarily sets umask 0077 around socket creation so the
// Unix domain socket inode never exists with broader permissions before chmod.
func withPrivateUmask(fn func() error) error {
	mcpUmaskMu.Lock()
	defer mcpUmaskMu.Unlock()
	prev := syscall.Umask(0o077)
	defer syscall.Umask(prev)
	return fn()
}

func isSocketMode(m os.FileMode) bool {
	return m&os.ModeSocket != 0
}

func isStaleSocketDialError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist)
}

// sameSocketFile reports whether path still refers (without following symlinks)
// to the Unix domain socket inode bound by this server instance.
func sameSocketFile(path string, expected os.FileInfo) bool {
	if expected == nil || path == "" {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !isSocketMode(info.Mode()) {
		return false
	}
	return os.SameFile(expected, info)
}
