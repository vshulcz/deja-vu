//go:build windows

package main

import "syscall"

// processAlive reports whether pid still names a running process. OpenProcess
// succeeding is not enough — a pid stays openable after exit while any handle
// to it is held, which is exactly the situation around a leaked-handle orphan
// — so the exit code is what answers. A process that chose STILL_ACTIVE (259)
// as its exit code reads as running, which fails safe the same way PID reuse
// does: the server keeps waiting (#2397).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const processQueryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// A process this one may not open still exists — the unix side reads
		// EPERM the same way.
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	const stillActive = 259
	return code == stillActive
}
