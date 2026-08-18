//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// terminalWidth asks the console how wide it is, in columns. The unix side does
// this with TIOCGWINSZ; here it is GetConsoleScreenBufferInfo, called through
// kernel32 directly because this module has no dependencies (#604).
//
// Anything other than a console — a pipe, a file, a redirect — reports false and
// the caller keeps its old answer.
func terminalWidth() (int, bool) {
	var info struct {
		size              struct{ x, y int16 }
		cursor            struct{ x, y int16 }
		attributes        uint16
		left, top         int16
		right, bottom     int16
		maximumWindowSize struct{ x, y int16 }
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetConsoleScreenBufferInfo")
	ret, _, _ := proc.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		return 0, false
	}
	// The window, not the buffer: a console's scrollback buffer is often wider
	// than the window showing it, and text is wrapped to the window.
	width := int(info.right) - int(info.left) + 1
	if width <= 0 {
		return 0, false
	}
	return width, true
}
