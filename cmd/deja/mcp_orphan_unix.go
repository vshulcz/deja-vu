//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether pid still names a process: signal 0 probes
// without delivering, because FindProcess accepts any pid on unix. EPERM still
// proves the process exists. PID reuse can make a departed parent look alive,
// which fails safe by leaving the server waiting rather than ending a possibly
// healthy connection (#2397).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Release frees the handle; nothing can be done if it fails and the answer
	// below is what the caller asked for.
	defer func() { _ = p.Release() }()
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}
