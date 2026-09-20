package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
)

// The remote half of a sync narrates its own work, and all of it used to arrive
// at once when the ssh command exited — which on a large import is the moment
// it stops mattering. These two pieces carry it out line by line instead
// (#3801).
//
// It is a package-level sink rather than another argument because `sshRunner`
// is the seam every sync test swaps, and a swapped runner returns its whole
// output at once, which is what a test wants. A test that replaces the runner
// therefore sees no streaming and needs no change; the real runner honours the
// sink.
var (
	lineSinkMu sync.Mutex
	lineSink   func(string)
)

func currentLineSink() func(string) {
	lineSinkMu.Lock()
	defer lineSinkMu.Unlock()
	return lineSink
}

// streamRemoteLines makes the remote's output arrive as it happens, prefixed
// with the machine that wrote it. The returned function stops the streaming and
// reports whether anything was printed, so the caller does not print the same
// sentences again at the end.
func streamRemoteLines(host string) func() bool {
	var mu sync.Mutex
	shown := 0
	lineSinkMu.Lock()
	lineSink = func(line string) {
		mu.Lock()
		defer mu.Unlock()
		// Bounded in two directions: each line is sanitised and cut the way
		// every other remote sentence is, and a remote that will not stop
		// talking cannot fill this terminal.
		if shown >= remoteStreamLineMax {
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		shown++
		fmt.Fprintf(os.Stdout, "%s: %s\n", hostForEcho(host), remoteOutputForEcho(line))
	}
	lineSinkMu.Unlock()
	return func() bool {
		lineSinkMu.Lock()
		lineSink = nil
		lineSinkMu.Unlock()
		mu.Lock()
		defer mu.Unlock()
		return shown > 0
	}
}

// remoteStreamLineMax is how many lines of a remote's narration reach this
// terminal. A sync prints a handful; the cap is what keeps a misbehaving or
// hostile peer from owning the screen.
const remoteStreamLineMax = 50

// lineWriter turns a stream of writes into whole lines. ssh hands over
// whatever arrived in one read, which splits mid-line as often as not.
type lineWriter struct {
	on  func(string)
	buf bytes.Buffer
}

func newLineWriter(on func(string)) *lineWriter { return &lineWriter{on: on} }

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := string(w.buf.Next(i + 1))
		w.on(strings.TrimRight(line, "\r\n"))
	}
	// A line the remote never terminated is held rather than dropped: the last
	// thing an interrupted import printed is the line worth reading.
	if w.buf.Len() > remoteStreamHoldMax {
		w.on(w.buf.String())
		w.buf.Reset()
	}
	return len(p), nil
}

// remoteStreamHoldMax bounds what is held waiting for a newline, so a remote
// that writes one enormous unterminated line cannot grow this buffer without
// limit.
const remoteStreamHoldMax = 64 << 10
