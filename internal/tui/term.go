package tui

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotTerminal is Open's answer when stdin or stdout is not a terminal, or
// the platform has no raw mode this package knows how to set.
var ErrNotTerminal = errors.New("not a terminal")

// Term owns the terminal while the screen is up: raw input, the alternate
// screen, and the last frame drawn, so the next one writes only its changes.
type Term struct {
	in, out *os.File
	Mode    Mode
	events  chan Event
	stop    chan struct{}
	wg      sync.WaitGroup
	restore func()
	prev    []string
	mu      sync.Mutex
	closed  bool
}

const (
	enterSeq = "\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[?1000h\x1b[?1006h\x1b[?2004h\x1b[H\x1b[2J"
	leaveSeq = "\x1b[?2004l\x1b[?1006l\x1b[?1000l\x1b[?7h\x1b[?25h\x1b[0m\x1b[?1049l"
)

// Open takes the terminal. Close gives it back, and must run on every path,
// including a panic, or the shell is left without echo.
func Open() (*Term, error) {
	in, out := os.Stdin, os.Stdout
	restore, err := makeRaw(in, out)
	if err != nil {
		return nil, err
	}
	t := &Term{in: in, out: out, Mode: DetectMode(), events: make(chan Event, 64), stop: make(chan struct{}), restore: restore}
	io.WriteString(out, enterSeq)
	t.wg.Add(2)
	go t.readLoop()
	go t.watchSize()
	return t, nil
}

// Events delivers keys, mouse, pastes and resizes, in order.
func (t *Term) Events() <-chan Event { return t.events }

// Size is the terminal's columns and rows, 80x24 when it will not say.
func (t *Term) Size() (int, int) {
	w, h, ok := size(t.out)
	if !ok || w < 1 || h < 1 {
		return 80, 24
	}
	return w, h
}

// Draw writes the rows of c that differ from the last frame, inside a
// synchronized update so the terminal shows the frame whole or not at all.
func (t *Term) Draw(c *Canvas) {
	lines := c.Lines(t.Mode)
	var b strings.Builder
	b.WriteString("\x1b[?2026h")
	if len(t.prev) != len(lines) {
		t.prev = make([]string, len(lines))
		b.WriteString("\x1b[0m\x1b[2J")
	}
	for y, l := range lines {
		if t.prev[y] == l {
			continue
		}
		b.WriteString("\x1b[" + strconv.Itoa(y+1) + ";1H" + l)
		t.prev[y] = l
	}
	b.WriteString("\x1b[?2026l")
	io.WriteString(t.out, b.String())
}

// Invalidate forgets the last frame, so the next Draw writes everything.
func (t *Term) Invalidate() { t.prev = nil }

// Write sends a raw sequence, such as an OSC 52 copy.
func (t *Term) Write(s string) { io.WriteString(t.out, s) }

// Close restores the terminal. It is safe to call more than once.
func (t *Term) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	close(t.stop)
	t.wg.Wait()
	io.WriteString(t.out, leaveSeq)
	t.restore()
}

func (t *Term) send(ev Event) {
	select {
	case t.events <- ev:
	case <-t.stop:
	}
}

// readLoop reads until Close. The read itself returns every ~100ms with
// nothing (VTIME on unix, a wait on Windows) so the loop can notice the stop
// and leave the terminal to whatever runs next: a goroutine parked in a read
// would eat the first key typed into the agent deja hands over to.
func (t *Term) readLoop() {
	defer t.wg.Done()
	buf := make([]byte, 4096)
	var rest []byte
	for {
		select {
		case <-t.stop:
			return
		default:
		}
		n, err := readTimeout(t.in, buf, 100*time.Millisecond)
		if n == 0 {
			if len(rest) > 0 {
				for _, ev := range Flush(rest) {
					t.send(ev)
				}
				rest = nil
			}
			if err != nil && err != io.EOF && err != errTimeout {
				return
			}
			continue
		}
		data := append(rest, buf[:n]...)
		evs, r := Decode(data)
		rest = append([]byte(nil), r...)
		// A read that ends on a bare ESC is the key: terminals write a whole
		// sequence in one go, so the rest of one is not still on its way.
		if len(rest) == 1 && rest[0] == 0x1b {
			evs = append(evs, Flush(rest)...)
			rest = nil
		}
		for _, ev := range evs {
			t.send(ev)
		}
	}
}

var errTimeout = errors.New("timeout")
