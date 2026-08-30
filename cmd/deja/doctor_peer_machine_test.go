package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/peers"
)

// A peer has two names: the ssh host it was added under, and the name the
// machine calls itself. Recall lines and `deja last` print the second; doctor
// printed only the first, so the row about "quicksilver" was headed
// "vlad@10.0.0.7" and nothing on any surface joined them (#2415).
func TestThePeerRowNamesTheMachineToo(t *testing.T) {
	now := time.Now()
	p := peers.Peer{
		Host:     "vlad@10.0.0.7",
		Machine:  "quicksilver",
		LastPull: now.Add(-2 * time.Hour),
	}
	line := peerLine(p, 3, now)
	if !strings.Contains(line, "quicksilver") {
		t.Errorf("the row about quicksilver's work does not name quicksilver: %q", line)
	}

	// A machine that calls itself what it was added as says it once.
	same := peers.Peer{Host: "mini", Machine: "mini", LastPull: now.Add(-time.Hour)}
	if got := peerLine(same, 1, now); strings.Count(got, "mini") != 0 {
		t.Errorf("the row repeats a name the header already carries: %q", got)
	}

	// The user part of an ssh target is not part of the name.
	prefixed := peers.Peer{Host: "vlad@mini", Machine: "mini", LastPull: now.Add(-time.Hour)}
	if got := peerLine(prefixed, 1, now); strings.Contains(got, "calls itself") {
		t.Errorf("the row says a name twice: %q", got)
	}

	// Nothing to add when the machine has never said what it is called.
	unknown := peers.Peer{Host: "build-box", LastPull: now.Add(-time.Hour)}
	if got := peerLine(unknown, 0, now); strings.Contains(got, "(") {
		t.Errorf("the row invented a name: %q", got)
	}
}
