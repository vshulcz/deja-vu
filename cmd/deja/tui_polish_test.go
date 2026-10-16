package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/tui"
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

// Prose in the reader wraps at a reading measure, not at the terminal's edge.
func TestTUIReaderMeasure(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	d := a.details[sessionKey(a.rows[0].s)]
	d.full.Messages[len(d.full.Messages)-1].Text = strings.Repeat("rotate the signing key ", 30)
	a.openReader()
	widest := 0
	for _, ln := range strings.Split(screen(a.frame(200, 30)), "\n")[1:] {
		if strings.Contains(ln, "rotate") {
			widest = max(widest, termwidth.Columns(strings.TrimRight(ln, " ")))
		}
	}
	if widest == 0 || widest > readerMeasure+4 {
		t.Errorf("widest prose line = %d columns", widest)
	}
}

// The query's words are lit in a card's headline and in ASKED, not only in the
// matched line under them.
func TestTUIHeadlineLightsTheQuery(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("webhook")
	hits, err := tuiSearch(dir, search.Options{Query: "webhook", Limit: 80})
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits = %d, %v", len(hits), err)
	}
	a.setRows(nil, hits, len(hits))
	a.p.mono = true // a match is drawn reversed, which the plain text shows
	c := a.frame(120, 36)
	lit := 0
	for y, ln := range c.Lines(tui.ModeMono) {
		if strings.Contains(c.Text(y), "webhook signature fails") && strings.Contains(ln, ";7m") {
			lit++
		}
	}
	// The headline, ASKED and MATCHED; only MATCHED was before.
	if lit != 3 {
		t.Errorf("rows lit = %d, want 3", lit)
	}
}
