package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// waitLine is what a command says while another deja holds the index. At a
// terminal the line keeps moving and counts the seconds, so a handoff that
// waits out a rebuild does not look hung; anywhere else it is one plain line.
type waitLine struct {
	w     io.Writer
	live  bool
	start time.Time
	stop  chan struct{}
	done  chan struct{}
	width int
}

// installLockWait takes stderr at the moment of the wait, not at start: the
// screen swaps it for the null device while it is up, and a line spinning on
// the real one would draw over the frame.
func installLockWait() {
	l := &waitLine{}
	index.LockWaitNotice = func() {
		l.w, l.live = os.Stderr, briefWanted(os.Stderr)
		l.begin()
	}
	index.LockWaitDone = l.end
}

const waitText = "another deja is building the index, waiting for it"

func (l *waitLine) begin() {
	if !l.live {
		fmt.Fprintln(l.w, "deja: another deja is building the index — waiting for it to finish")
		return
	}
	l.start = time.Now()
	l.stop, l.done = make(chan struct{}), make(chan struct{})
	l.draw(0)
	go func() {
		defer close(l.done)
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for i := 1; ; i++ {
			select {
			case <-l.stop:
				return
			case <-t.C:
				l.draw(i)
			}
		}
	}()
}

func (l *waitLine) draw(i int) {
	spin := []string{"◐", "◓", "◑", "◒"}[i%4]
	l.put("deja: " + spin + " " + waitText + " · " + num(int(time.Since(l.start).Seconds())) + "s")
}

// put rewrites the line in place. Padding with spaces rather than an erase
// sequence keeps it clean on a console that does not take escapes.
func (l *waitLine) put(s string) {
	n := len([]rune(s))
	pad := ""
	if l.width > n {
		pad = strings.Repeat(" ", l.width-n)
	}
	l.width = n
	fmt.Fprint(l.w, "\r"+s+pad)
}

func (l *waitLine) end() {
	if !l.live || l.stop == nil {
		return
	}
	close(l.stop)
	<-l.done
	l.put("deja: ✓ the index is ready, waited " + formatSeconds(time.Since(l.start)))
	fmt.Fprintln(l.w)
}
