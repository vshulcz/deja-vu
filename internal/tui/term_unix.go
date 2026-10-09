//go:build darwin || dragonfly || freebsd || netbsd || openbsd || linux

package tui

import (
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) syscall.Errno {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg))
	return e
}

// makeRaw is cfmakeraw, plus VMIN=0 VTIME=1: a read returns after a tenth of
// a second with nothing, which is what lets the reader stop (see readLoop).
func makeRaw(in, out *os.File) (func(), error) {
	if _, _, ok := size(out); !ok {
		return nil, ErrNotTerminal
	}
	var old syscall.Termios
	if e := ioctl(in, ioctlGetTermios, unsafe.Pointer(&old)); e != 0 {
		return nil, ErrNotTerminal
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 0
	raw.Cc[syscall.VTIME] = 1
	if e := ioctl(in, ioctlSetTermios, unsafe.Pointer(&raw)); e != 0 {
		return nil, ErrNotTerminal
	}
	return func() { ioctl(in, ioctlSetTermios, unsafe.Pointer(&old)) }, nil
}

func size(f *os.File) (int, int, bool) {
	var ws struct{ rows, cols, xpixel, ypixel uint16 }
	if e := ioctl(f, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); e != 0 || ws.cols == 0 {
		return 0, 0, false
	}
	return int(ws.cols), int(ws.rows), true
}

func readTimeout(f *os.File, buf []byte, _ time.Duration) (int, error) {
	n, err := f.Read(buf)
	if n == 0 && err == io.EOF {
		return 0, errTimeout
	}
	return n, err
}

func (t *Term) watchSize() {
	defer t.wg.Done()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	for {
		select {
		case <-t.stop:
			return
		case <-ch:
			w, h := t.Size()
			t.send(Event{Kind: EvResize, W: w, H: h})
		}
	}
}
