//go:build windows

package main

import (
	"errors"
	"os"
	"syscall"
)

func withPrivateUmask(fn func() error) error {
	return fn()
}

func isSocketMode(m os.FileMode) bool {
	return m&(os.ModeSocket|os.ModeIrregular) != 0 && !m.IsRegular() && !m.IsDir() && m&os.ModeSymlink == 0
}

func isStaleSocketDialError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.Errno(10061)) || errors.Is(err, os.ErrNotExist)
}

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
