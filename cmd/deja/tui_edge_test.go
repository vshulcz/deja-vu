package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
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

// A session picked on the screen is handed on from the index as it is. A
// refresh there waited out a whole rebuild after an upgrade, under a closed
// screen, before the agent opened.
func TestTUIHandsOnWithoutARefresh(t *testing.T) {
	dir, root := tuiStore(t)
	write := func(id, at, text string) {
		user, _ := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": "/work/payments", "timestamp": at,
			"message": map[string]any{"role": "user", "content": text}})
		writeClaudeFixture(t, filepath.Join(root, "payments", id+".jsonl"), id, []string{string(user)})
	}
	// A newer session whose id the picked one's is a prefix of.
	write("c3333333-hookx", "2026-03-05T10:00:00Z", "a newer session under a longer id")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	write("d4444444-new", "2026-03-06T10:00:00Z", "a session that arrived after the screen opened")
	picked, ok, err := index.FindByIdentity(dir, "claude", "c3333333-hook")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	pickedOnScreen = &picked
	t.Cleanup(func() { pickedOnScreen = nil })
	if s, ok, err := findByPrefix(dir, "c3333333-hook"); err != nil || !ok || s.ID != "c3333333-hook" {
		t.Fatalf("findByPrefix = %v %v %v", s.ID, ok, err)
	}
	if s, err := handoffSource(dir, "c3333333-hook"); err != nil || s.ID != "c3333333-hook" {
		t.Fatalf("handoffSource = %v %v", s.ID, err)
	}
	if _, ok, _ := index.FindByPrefix(dir, "d4444444"); ok {
		t.Error("handing on a picked session refreshed the index first")
	}
	pickedOnScreen = nil
	if _, ok, _ := findByPrefix(dir, "d4444444"); !ok {
		t.Error("from a shell the lookup no longer brings the index up to date")
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
