package main

import (
	"fmt"
	"io"
	"os"
)

// The hooks and the status line print without a final newline, because a host
// reads those bytes as they are. Run by hand, the shell prompt then lands on
// the same line as `</deja-recall>`. endLineOnTerminal adds the newline only
// when stdout is a terminal, which no host ever is, so what a host reads does
// not change.

type lineEndWriter struct {
	w    io.Writer
	last byte
	n    int
}

func (l *lineEndWriter) Write(p []byte) (int, error) {
	n, err := l.w.Write(p)
	if n > 0 {
		l.last = p[n-1]
		l.n += n
	}
	return n, err
}

func endLineOnTerminal(f *os.File, run func(io.Writer) error) error {
	if !briefWanted(f) {
		return run(f)
	}
	lw := &lineEndWriter{w: f}
	err := run(lw)
	if lw.n > 0 && lw.last != '\n' {
		fmt.Fprintln(f)
	}
	return err
}
