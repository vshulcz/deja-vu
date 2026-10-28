//go:build windows

package main

import (
	"errors"
	"strconv"
	"syscall"
)

// processStart reads the creation time of pid. Whether it is still running is
// processAlive's answer — OpenProcess plus GetExitCodeProcess, since a pid
// stays openable after exit while anyone holds a handle to it. The handle
// opened here is closed before returning.
func processStart(pid int) (start string, alive, known bool) {
	if pid <= 0 {
		return "", false, true
	}
	if !processAlive(pid) {
		return "", false, true
	}
	const processQueryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid)) //nolint:gosec // pid > 0 is checked above and windows pids fit in a DWORD
	if err != nil {
		// Exited between the two calls: ERROR_INVALID_PARAMETER is what a
		// pid that names nothing gets.
		const errorInvalidParameter = syscall.Errno(87)
		if errors.Is(err, errorInvalidParameter) {
			return "", false, true
		}
		// Running, but not ours to open: no start time to compare, so the
		// claim falls back to its age.
		return "", true, false
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", true, false
	}
	return strconv.FormatInt(created.Nanoseconds(), 10), true, true
}
