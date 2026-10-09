package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/tui"
)

func TestTUIReaderFindAndTurns(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	s := model.Session{ID: "turns-1", Harness: "claude"}
	for i := 0; i < 12; i++ {
		text := "step " + num(i+1) + " of the migration"
		if i == 4 || i == 9 {
			text += ", the flaky cutover"
		}
		s.Messages = append(s.Messages,
			model.Message{Role: "user", Text: text},
			model.Message{Role: "assistant", Text: strings.Repeat("checked it\n", 4)})
	}
	a.view, a.reader = viewReader, readerState{s: s, d: &tuiDetail{full: s}}
	key := func(r rune) { a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r}) }
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 1 of 12", "[ ]  turns", "/  find")

	key(']')
	key(']')
	sc := screen(a.frame(100, 24))
	wantOnScreen(t, sc, "turn 3 of 12", "step 3 of the migration")
	key('[')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 2 of 12")
	// Scrolled into the middle of a turn, [ goes back to its start.
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyDown})
	key('[')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 2 of 12")

	// / opens the line; letters type into it rather than acting.
	key('/')
	wantOnScreen(t, screen(a.frame(100, 24)), "find in this session", "esc  close")
	for _, r := range "cutover" {
		key(r)
	}
	if a.view != viewReader || string(a.reader.find) != "cutover" {
		t.Fatalf("find typed %q", string(a.reader.find))
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	sc = screen(a.frame(100, 24))
	wantOnScreen(t, sc, "hit 1 of 2", "turn 5 of 12", "step 5 of the migration, the flaky cutover")
	key('n')
	wantOnScreen(t, screen(a.frame(100, 24)), "hit 2 of 2", "turn 10 of 12")

	// A word that is not there keeps the matches it had.
	key('/')
	for _, r := range "kafka" {
		key(r)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if a.reader.finding || len(a.reader.hits) != 2 || !strings.Contains(a.toast, "kafka") {
		t.Errorf("a miss: finding %v, hits %d, toast %q", a.reader.finding, len(a.reader.hits), a.toast)
	}
	// esc closes the line and leaves the reader open.
	key('/')
	a.handle(tui.Event{Kind: tui.EvPaste, Text: "x y"})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyBackspace})
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	if a.reader.finding || a.view != viewReader {
		t.Error("esc closes the / line only")
	}
	// Scrolled to the end, the last turn is the one on screen.
	key('G')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 12 of 12")
	key('[')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 11 of 12")
	key(']')
	key(']')
	wantOnScreen(t, screen(a.frame(100, 24)), "turn 12 of 12")
	key('?')
	wantOnScreen(t, screen(a.frame(100, 30)), "find in the session", "next / previous turn")
}
