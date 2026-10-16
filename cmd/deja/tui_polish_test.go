package main

import (
	"strings"
	"testing"
)

// At 80 columns the hints ran out before the way out; it keeps the right edge
// now, and the hints between give way first.
func TestTUIFooterKeepsTheWayOut(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	lines := strings.Split(screen(a.frame(80, 24)), "\n")
	if foot := lines[23]; !strings.HasSuffix(strings.TrimRight(foot, " "), "esc  quit") {
		t.Errorf("list footer = %q", foot)
	}
	a.openReader()
	lines = strings.Split(screen(a.frame(80, 24)), "\n")
	if foot := lines[23]; !strings.HasSuffix(strings.TrimRight(foot, " "), "esc  back") || !strings.Contains(foot, "scroll") {
		t.Errorf("reader footer = %q", foot)
	}
}
