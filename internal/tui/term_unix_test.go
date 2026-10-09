//go:build darwin || dragonfly || freebsd || netbsd || openbsd || linux

package tui

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func next(t *testing.T, term *Term) Event {
	t.Helper()
	select {
	case ev := <-term.Events():
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
	return Event{}
}

// The reader turns what arrives into events, a bare ESC into the key, a
// SIGWINCH into a resize, and stops when the terminal is closed.
func TestReadLoopAndClose(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	term := &Term{in: r, out: out, events: make(chan Event, 8), stop: make(chan struct{}), restore: func() { restored = true }}
	term.wg.Add(2)
	go term.readLoop()
	go term.watchSize()

	w.Write([]byte("x\x1b[A"))
	if ev := next(t, term); ev.Rune != 'x' {
		t.Errorf("first = %+v", ev)
	}
	if ev := next(t, term); ev.Key != KeyUp {
		t.Errorf("second = %+v", ev)
	}
	w.Write([]byte("\x1b"))
	if ev := next(t, term); ev.Key != KeyEsc {
		t.Errorf("a lone ESC = %+v", ev)
	}
	w.Write([]byte("\x1b["))
	time.Sleep(50 * time.Millisecond)
	w.Write([]byte("B"))
	if ev := next(t, term); ev.Key != KeyDown {
		t.Errorf("a sequence split over two reads = %+v", ev)
	}
	syscall.Kill(os.Getpid(), syscall.SIGWINCH)
	if ev := next(t, term); ev.Kind != EvResize || ev.W != 80 || ev.H != 24 {
		t.Errorf("resize = %+v", ev)
	}
	w.Close()
	term.Close()
	term.Close()
	if !restored {
		t.Error("Close must restore the terminal")
	}
	b, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(b), leaveSeq) {
		t.Error("Close leaves the alternate screen")
	}
	if w, h := term.Size(); w != 80 || h != 24 {
		t.Errorf("a file reports the 80x24 default, got %dx%d", w, h)
	}
}

func TestOpenRefusesAFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := makeRaw(f, f); err != ErrNotTerminal {
		t.Errorf("makeRaw on a file = %v", err)
	}
	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old }()
	if _, err := Open(); err != ErrNotTerminal {
		t.Errorf("Open with a file for stdout = %v", err)
	}
}
