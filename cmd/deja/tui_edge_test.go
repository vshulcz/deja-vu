package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// Overtaken before it ran, the search is not run at all.
	time.Sleep(200 * time.Millisecond)
	select {
	case f := <-a.updates:
		f()
	default:
	}
	if len(a.rows) != 3 {
		t.Errorf("home has %d rows after a late search", len(a.rows))
	}
}

// A word typed at speed runs one search, not one per letter: on a large
// history each was a full pass and together they held the screen still.
func TestTUITypingRunsTheLastSearchOnly(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	for _, r := range "webhook" {
		a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r})
	}
	time.Sleep(400 * time.Millisecond)
	n := 0
	for len(a.updates) > 0 {
		(<-a.updates)()
		n++
	}
	if n != 1 || a.searching || len(a.rows) == 0 {
		t.Errorf("%d searches answered, searching=%v, %d rows", n, a.searching, len(a.rows))
	}
}

// The empty box takes ? as help, as the footer says.
func TestTUIQuestionMarkOpensHelp(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: '?'})
	if a.modal != modalHelp || len(a.query) != 0 {
		t.Errorf("modal=%d query=%q", a.modal, string(a.query))
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

// A refresh of the same search keeps the reader's pick, and a first build
// finishing under an early start keeps the search on screen.
func TestTUIRefreshKeepsWhatIsOnScreen(t *testing.T) {
	dir, root := tuiStore(t)
	// Asked twice, so the search names the newest and selects it.
	user, _ := json.Marshal(map[string]any{"type": "user", "sessionId": "e5555555-again", "cwd": "/work/payments", "timestamp": "2026-03-04T10:00:00Z",
		"message": map[string]any{"role": "user", "content": "the retry double charges the card again"}})
	writeClaudeFixture(t, filepath.Join(root, "payments", "e5555555-again.jsonl"), "e5555555-again", []string{string(user)})
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	a := newTestTUI(t, dir)
	a.query = []rune("retry card")
	a.startSearch()
	drain(t, a)
	if len(a.rows) < 2 {
		t.Fatalf("%d rows", len(a.rows))
	}
	a.sel = (a.sel + 1) % len(a.rows)
	picked := sessionKey(a.rows[a.sel].s)
	a.reload()
	drain(t, a)
	if got := sessionKey(a.rows[a.sel].s); got != picked {
		t.Errorf("a refresh moved the pick from %q to %q", picked, got)
	}

	a.welcome.early = true
	a.firstBuild()
	drain(t, a)
	if string(a.query) != "retry card" || a.listed != num(a.scope)+"\x00retry card" {
		t.Errorf("the finished build replaced the search: %q %q", string(a.query), a.listed)
	}
}

// A session that grew after its first read is read again.
func TestTUIGrownSessionIsReadAgain(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	s := a.rows[0].s
	delete(a.loading, sessionKey(s))
	a.want(s)
	if a.loading[sessionKey(s)] {
		t.Fatal("an unchanged session was read twice")
	}
	s.Updated = s.Updated.Add(time.Minute)
	a.want(s)
	if !a.loading[sessionKey(s)] {
		t.Error("a session that grew keeps its first read")
	}
}
