package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/tui"
)

// Every view and modal draws at any size, down to a single cell.
func TestTUITinyTerminal(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	for _, m := range []int{modalNone, modalContinue, modalAgents, modalHelp, modalPalette} {
		a.modal = modalNone
		if m != modalNone {
			a.openModal(m)
		}
		for _, sz := range [][2]int{{1, 1}, {2, 2}, {5, 3}, {20, 5}, {40, 10}} {
			a.frame(sz[0], sz[1])
		}
	}
}

// A search that lands after the box was cleared does not replace the home
// list.
func TestTUILateSearchAfterClear(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("idempotency")
	a.startSearch()
	a.query = nil
	a.loadHome()
	drain(t, a)
	if len(a.rows) != 3 {
		t.Errorf("home has %d rows after a late search", len(a.rows))
	}
}

func TestTUIReaderHitAfterResize(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.query = []rune("webhook")
	for i, r := range a.rows {
		if r.s.ID == "c3333333-hook" {
			a.sel = i
		}
	}
	a.openReader()
	a.frame(120, 30)
	a.reader.hit, a.reader.layoutW = 7, 0
	s := screen(a.frame(160, 30))
	if strings.Contains(s, "hit 8") {
		t.Errorf("the hit counter ran past the hits:\n%s", s)
	}
}

func TestTUIPasteIntoModal(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.openContinue()
	a.handle(tui.Event{Kind: tui.EvPaste, Text: "co\ndex"})
	if string(a.m.filter) != "co dex" {
		t.Errorf("filter = %q", string(a.m.filter))
	}
	a.modal = modalHelp
	a.handle(tui.Event{Kind: tui.EvPaste, Text: "x"})
	if a.modal != modalHelp {
		t.Error("a paste closed help")
	}
}
