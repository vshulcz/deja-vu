package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// A sync that says nothing for minutes reads as a hang, and the report at the
// end is what a later performance question is answered from (#3801).
func TestSyncPhasesSayWhatIsRunningAndWhereTheTimeWent(t *testing.T) {
	buf := &lockedBuffer{}
	p := newSyncPhases(buf)
	p.beat = 20 * time.Millisecond
	p.floor = time.Millisecond

	p.start("exporting records")
	p.note("12 MiB in 3 batches")
	// Long enough for the heartbeat to fire at least once.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "still exporting") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	p.start("importing on mini")
	time.Sleep(3 * time.Millisecond)
	// The report is about the run, so a fast run gets none of it: the threshold
	// is the heartbeat, which this stand has shortened.
	p.summary("pushed to mini")

	got := buf.String()
	for _, want := range []string{
		"deja: exporting records…",
		"deja:   12 MiB in 3 batches",
		"still exporting records…",
		"deja: importing on mini…",
		"pushed to mini in ",
		"exporting records ",
		"importing on mini ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The phases are reported in the order they ran, which is what makes the
	// line readable as a timeline.
	if i, j := strings.Index(got, "— exporting records"), strings.Index(got, "importing on mini "); i < 0 || j < i {
		t.Errorf("the report is not in phase order:\n%s", got)
	}
	// Nothing keeps printing after the run ends.
	before := len(buf.String())
	time.Sleep(60 * time.Millisecond)
	if got := buf.String(); len(got) != before {
		t.Errorf("a heartbeat outlived the phase: %q", got[before:])
	}
}

// A quick exchange gets no report at all: two lines about two seconds is noise
// on the one path that runs from launchd every hour.
func TestAQuickSyncPrintsNoReport(t *testing.T) {
	buf := &lockedBuffer{}
	p := newSyncPhases(buf)
	p.start("exporting records")
	p.summary("pushed to mini")
	if strings.Contains(buf.String(), "pushed to mini in") {
		t.Errorf("a two-millisecond sync reported its timings: %q", buf.String())
	}
}

// ssh hands over whatever arrived in one read, which splits mid-line as often
// as not, so the remote's narration has to be reassembled before it is echoed.
func TestRemoteOutputIsEchoedLineByLine(t *testing.T) {
	var got []string
	w := newLineWriter(func(line string) { got = append(got, line) })
	for _, chunk := range []string{"deja: imp", "orted 5 records\r\ndeja: 2 ses", "sions\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "deja: imported 5 records" || got[1] != "deja: 2 sessions" {
		t.Fatalf("lines = %q", got)
	}
	// An unterminated tail is held, not printed as a fragment...
	if _, err := w.Write([]byte("deja: still going")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("a fragment was echoed: %q", got)
	}
	// ...unless it grows past the bound, since a remote that writes one
	// enormous line must not grow this buffer without limit.
	if _, err := w.Write(bytes.Repeat([]byte("x"), remoteStreamHoldMax+1)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("the held line was never flushed: %d lines", len(got))
	}
}

// The remote is another machine, so what it prints is bounded and sanitised
// the way every other remote sentence is — and a line already shown as it
// arrived must not be printed again at the end.
func TestStreamedRemoteLinesAreBoundedAndCounted(t *testing.T) {
	var counted bool
	got := captureStdout(t, func() {
		stop := streamRemoteLines("mini")
		sink := currentLineSink()
		if sink == nil {
			t.Error("nothing is streaming")
			return
		}
		sink("deja: imported 5 records\x1b[31m")
		sink("   ")
		for i := 0; i < remoteStreamLineMax+10; i++ {
			sink("chatter")
		}
		counted = stop()
	})
	if !counted {
		t.Error("the streamed lines were not counted, so the caller will print them twice")
	}
	if currentLineSink() != nil {
		t.Error("the sink outlived the phase")
	}
	if !strings.Contains(got, "mini: deja: imported 5 records") {
		t.Errorf("the remote's line did not arrive: %q", got)
	}
	if strings.Contains(got, "\x1b[31m") {
		t.Errorf("an escape from the remote reached the terminal: %q", got)
	}
	if n := strings.Count(got, "chatter"); n > remoteStreamLineMax {
		t.Errorf("a talkative remote printed %d lines", n)
	}
	if strings.Contains(got, "mini: \n") {
		t.Errorf("a blank remote line was echoed: %q", got)
	}
}

// lockedBuffer is a buffer this test can read while the heartbeat goroutine
// writes to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
