//go:build windows

package tui

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGetMode      = kernel32.NewProc("GetConsoleMode")
	procSetMode      = kernel32.NewProc("SetConsoleMode")
	procBufferInfo   = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procWaitForInput = kernel32.NewProc("WaitForSingleObject")
)

const (
	enableProcessedInput  = 0x1
	enableLineInput       = 0x2
	enableEchoInput       = 0x4
	enableQuickEdit       = 0x40
	enableExtendedFlags   = 0x80
	enableVTInput         = 0x200
	enableProcessedOutput = 0x1
	enableVTProcessing    = 0x4
	disableAutoReturn     = 0x8
)

func consoleMode(f *os.File) (uint32, bool) {
	var m uint32
	r, _, _ := procGetMode.Call(f.Fd(), uintptr(unsafe.Pointer(&m)))
	return m, r != 0
}

func setConsoleMode(f *os.File, m uint32) bool {
	r, _, _ := procSetMode.Call(f.Fd(), uintptr(m))
	return r != 0
}

// makeRaw turns line input and echo off and asks the console for VT
// sequences both ways, which is what Windows Terminal and conhost since 1809
// speak.
func makeRaw(in, out *os.File) (func(), error) {
	oldIn, ok1 := consoleMode(in)
	oldOut, ok2 := consoleMode(out)
	if !ok1 || !ok2 {
		return nil, ErrNotTerminal
	}
	rawIn := oldIn&^(enableProcessedInput|enableLineInput|enableEchoInput|enableQuickEdit) | enableVTInput | enableExtendedFlags
	if !setConsoleMode(in, rawIn) {
		return nil, ErrNotTerminal
	}
	if !setConsoleMode(out, oldOut|enableProcessedOutput|enableVTProcessing|disableAutoReturn) {
		setConsoleMode(in, oldIn)
		return nil, ErrNotTerminal
	}
	return func() {
		setConsoleMode(in, oldIn)
		setConsoleMode(out, oldOut)
	}, nil
}

func size(f *os.File) (int, int, bool) {
	var info struct {
		size              struct{ x, y int16 }
		cursor            struct{ x, y int16 }
		attributes        uint16
		left, top         int16
		right, bottom     int16
		maximumWindowSize struct{ x, y int16 }
	}
	r, _, _ := procBufferInfo.Call(f.Fd(), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, 0, false
	}
	w := int(info.right) - int(info.left) + 1
	h := int(info.bottom) - int(info.top) + 1
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// readTimeout waits on the console handle before reading, so the reader can
// see a stop instead of blocking until the next key.
func readTimeout(f *os.File, buf []byte, d time.Duration) (int, error) {
	const waitObject0 = 0
	r, _, _ := procWaitForInput.Call(f.Fd(), uintptr(d.Milliseconds()))
	if r != waitObject0 {
		return 0, errTimeout
	}
	return f.Read(buf)
}

// The console has no resize signal a program can wait on through the VT
// input, so the size is polled.
func (t *Term) watchSize() {
	defer t.wg.Done()
	w, h := t.Size()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			if nw, nh := t.Size(); nw != w || nh != h {
				w, h = nw, nh
				t.send(Event{Kind: EvResize, W: w, H: h})
			}
		}
	}
}
