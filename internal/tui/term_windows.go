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
	procPeekInput    = kernel32.NewProc("PeekConsoleInputW")
	procReadInput    = kernel32.NewProc("ReadConsoleInputW")
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

// inputRecord is INPUT_RECORD with the KEY_EVENT_RECORD arm of its union.
type inputRecord struct {
	eventType uint16
	_         uint16
	keyDown   int32
	repeat    uint16
	vk        uint16
	scan      uint16
	char      uint16
	state     uint32
}

const keyEvent = 0x1

// readTimeout waits on the console handle before reading, so the reader can
// see a stop instead of blocking until the next key.
//
// The handle is signalled by key-ups, focus and mouse records too, and a
// read on those blocks until a character is typed: Close then hung until the
// next key, and that key was lost. Records without a character are taken
// off the queue here, and the read happens only when one is waiting.
func readTimeout(f *os.File, buf []byte, d time.Duration) (int, error) {
	const waitObject0 = 0
	r, _, _ := procWaitForInput.Call(f.Fd(), uintptr(d.Milliseconds()))
	if r != waitObject0 {
		return 0, errTimeout
	}
	var recs [64]inputRecord
	var n uint32
	if ok, _, _ := procPeekInput.Call(f.Fd(), uintptr(unsafe.Pointer(&recs[0])), uintptr(len(recs)), uintptr(unsafe.Pointer(&n))); ok == 0 {
		return f.Read(buf)
	}
	first := -1
	for i := 0; i < int(n); i++ {
		if recs[i].eventType == keyEvent && recs[i].keyDown != 0 && recs[i].char != 0 {
			first = i
			break
		}
	}
	drop := func(k int) {
		if k > 0 {
			var got uint32
			_, _, _ = procReadInput.Call(f.Fd(), uintptr(unsafe.Pointer(&recs[0])), uintptr(k), uintptr(unsafe.Pointer(&got)))
		}
	}
	if first < 0 {
		drop(int(n))
		return 0, errTimeout
	}
	drop(first)
	// The runtime's console read takes ctrl-z for end of input and returns
	// nothing, so the key is read here.
	if recs[first].char == 0x1a {
		drop(1)
		buf[0] = 0x1a
		return 1, nil
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
