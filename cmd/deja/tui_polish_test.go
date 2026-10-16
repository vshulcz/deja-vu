package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
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

// The help columns are sized to what they hold. Thirds of the box cut most
// labels short at 80 columns and one at any width.
func TestTUIHelpLabelsWhole(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.modal = modalHelp
	for _, w := range []int{79, 80, 100, 140} {
		s := screen(a.frame(w, 24))
		wantOnScreen(t, s, "tab  this project / all / deleted", "n N  next / previous match",
			"R  put a deleted one back", "in:api one project", "esc  back to the list", "scroll anywhere.")
	}
	// Wide enough, the three sections sit side by side.
	s := screen(a.frame(140, 36))
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, "FIND") && !strings.Contains(ln, "ACT") {
			t.Errorf("sections not side by side: %q", ln)
		}
	}
}

// A deleted session among live ones says so beside its agent; its headline
// starts on the column every other headline does.
func TestTUIDeletedCardKeepsTheHeadlineColumn(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.keptIDs = map[string]bool{sessionKey(a.rows[0].s): true}
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "▌ webhook signature fails", "· deleted")
	a.setScope(scopeKept)
	a.kept, a.keptLoaded = []model.Session{a.allMeta[0]}, true
	a.loadHome()
	if s := screen(a.frame(120, 36)); strings.Contains(s, "· deleted") {
		t.Errorf("the Deleted tab says deleted on every card:\n%s", s)
	}
}

// The agent strip over this project's list counts this project, not the
// machine.
func TestTUIStripCountsTheScope(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	wantOnScreen(t, screen(a.frame(120, 36)), "Claude Code 3")
	a.projects = []string{"payments"}
	a.setScope(scopeHere)
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "Claude Code 2")
}
