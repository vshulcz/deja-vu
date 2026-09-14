package index

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// A store that reads slowly has to say so while it is happening. An index run
// over a 520 MB opencode store printed one line and then nothing for 13m54s,
// and nothing on screen said which store it was in or that it was still moving
// (#3555).
func TestAStoreThatReadsSlowlySaysSoWhileItIsReading(t *testing.T) {
	defer shortenSlowRead(t, 20*time.Millisecond, 20*time.Millisecond)()
	w := &safeBuilder{}
	stop := sayItIsStillReading("opencode", time.Now(), w)
	waitFor(t, func() bool { return strings.Contains(w.String(), "still reading") })
	stop()

	line := firstLine(w.String())
	if !strings.HasPrefix(line, "deja: opencode: still reading (") {
		t.Fatalf("line = %q — it has to name the store and that it is still moving", line)
	}
	// And it stops when the read lands, rather than narrating a store that is
	// already indexed.
	after := w.String()
	time.Sleep(80 * time.Millisecond)
	if w.String() != after {
		t.Fatalf("kept talking after the read landed:\n%s", w.String())
	}
}

// The notes pseudo-source narrates under the name a person knows it by.
func TestTheStillReadingLineUsesTheNameAPersonSees(t *testing.T) {
	defer shortenSlowRead(t, 10*time.Millisecond, 10*time.Millisecond)()
	w := &safeBuilder{}
	stop := sayItIsStillReading("deja", time.Now(), w)
	waitFor(t, func() bool { return strings.Contains(w.String(), "still reading") })
	stop()
	if !strings.Contains(w.String(), "deja: notes: still reading") {
		t.Fatalf("line = %q", firstLine(w.String()))
	}
}

// Nothing is said for a store that reads at the ordinary speed, and the wait
// that was worth reporting is reported.
func TestOnlyAWaitWorthReportingReachesTheLine(t *testing.T) {
	const line = "deja: opencode: 227 sessions, 3731 messages"
	if got := withReadTime(line, 900*time.Millisecond); got != line {
		t.Errorf("a fast read stamped its time: %q", got)
	}
	if got := withReadTime(line, 14*time.Minute); got != line+" — the read took 14m0s" {
		t.Errorf("got %q", got)
	}
}

// A suppressed narration stays suppressed: the terminal build paints its own
// display and a raw line would land in the middle of it.
func TestTheStillReadingLineRespectsASilencedRun(t *testing.T) {
	defer shortenSlowRead(t, 5*time.Millisecond, 5*time.Millisecond)()
	SuppressHarnessNarration = true
	defer func() { SuppressHarnessNarration = false }()
	w := &safeBuilder{}
	stop := sayItIsStillReading("opencode", time.Now(), w)
	time.Sleep(40 * time.Millisecond)
	stop()
	if w.String() != "" {
		t.Fatalf("wrote %q into a silenced run", w.String())
	}
}

func shortenSlowRead(t *testing.T, after, every time.Duration) func() {
	t.Helper()
	oldAfter, oldEvery := readSlowAfter, readStillReadingEvery
	readSlowAfter, readStillReadingEvery = after, every
	return func() { readSlowAfter, readStillReadingEvery = oldAfter, oldEvery }
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("nothing said while the read was still running")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// safeBuilder is the progress writer for these tests: the notice runs in its
// own goroutine, so the test reads what it wrote under the same lock.
type safeBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
