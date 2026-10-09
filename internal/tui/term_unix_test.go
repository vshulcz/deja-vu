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

	_, _ = w.Write([]byte("x\x1b[A"))
	if ev := next(t, term); ev.Rune != 'x' {
		t.Errorf("first = %+v", ev)
	}
	if ev := next(t, term); ev.Key != KeyUp {
		t.Errorf("second = %+v", ev)
	}
	_, _ = w.Write([]byte("\x1b"))
	if ev := next(t, term); ev.Key != KeyEsc {
		t.Errorf("a lone ESC = %+v", ev)
	}
	_, _ = w.Write([]byte("\x1b["))
	time.Sleep(50 * time.Millisecond)
	_, _ = w.Write([]byte("B"))
	if ev := next(t, term); ev.Key != KeyDown {
		t.Errorf("a sequence split over two reads = %+v", ev)
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGWINCH)
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

// A paste that arrives in two pieces with a pause between them, as it does
// over ssh, is still one paste: its tail must not run as keystrokes.
func TestReadLoopWaitsForASlowPaste(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	term := &Term{in: r, out: out, events: make(chan Event, 8), stop: make(chan struct{}), restore: func() {}}
	term.wg.Add(1)
	go term.readLoop()
	_, _ = w.Write([]byte("\x1b[200~line one\r"))
	time.Sleep(450 * time.Millisecond)
	_, _ = w.Write([]byte("rm -rf x\r\x1b[201~"))
	if ev := next(t, term); ev.Kind != EvPaste || ev.Text != "line one\rrm -rf x\r" {
		t.Errorf("slow paste = %+v", ev)
	}
	w.Close()
	close(term.stop)
	term.wg.Wait()
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
