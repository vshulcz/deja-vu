//go:build !windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// terminalWidth asks the terminal how wide it is, in columns.
//
// The brief and every other column-aligned screen used to assume 80 unless the
// reader exported COLUMNS, so a 60-column split pane — what an editor gives
// you — wrapped mid-word on the first screen a new user sees (#604). This is the
// ioctl every terminal program uses for it; the module has no dependencies, so
// it is spelled out here the way lock_unix.go spells out file locking.
//
// Not a terminal, or an ioctl that fails, reports false and the caller keeps its
// old answer.
func terminalWidth() (int, bool) {
	var ws struct {
		rows, cols, xpixel, ypixel uint16
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.cols == 0 {
		return 0, false
	}
	return int(ws.cols), true
}
