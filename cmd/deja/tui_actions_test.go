package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/tui"
)

// i and p copy the session's id and its project's path, from the list and the
// reader; F asks first and then forgets the session the way the command does.
func TestTUISessionActions(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	var copied string
	a.copy = func(s string) error { copied = s; return nil }
	a.forget = func(id string) error { return runForget(dir, []string{"--session", id}) }
	key := func(r rune) { a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: r}) }
	s := a.rows[0].s
	a.listFocus = true
	key('i')
	if copied != s.ID || !strings.Contains(a.toast, "Session id copied") {
		t.Errorf("i copied %q, toast %q", copied, a.toast)
	}
	a.openReader()
	a.frame(120, 36)
	key('p')
	if copied != s.Project || copied == "" || !strings.Contains(a.toast, "Project path copied") {
		t.Errorf("p copied %q, toast %q", copied, a.toast)
	}
	labels := ""
	for _, it := range a.paletteItems() {
		labels += it.key + " " + it.label + "\n"
	}
	for _, want := range []string{"i Copy the session id", "p Copy the project path", "F Forget this session…"} {
		if !strings.Contains(labels, want) {
			t.Errorf("palette lacks %q:\n%s", want, labels)
		}
	}
	// Esc on the question forgets nothing.
	key('F')
	if a.modal != modalForget {
		t.Fatal("F asks first")
	}
	sc := screen(a.frame(80, 24))
	wantOnScreen(t, sc, "Forget this session?", "own file stays", "Keep it", "▀")
	// The help names the three only under ^k; the keys still work.
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	a.openModal(modalHelp)
	help := screen(a.frame(120, 36))
	wantOnScreen(t, help, "copy id, path, forget…")
	if strings.Contains(help, "copy the session id") || strings.Contains(help, "forget it") {
		t.Errorf("help lists the small actions as keys:\n%s", help)
	}
	a.modal = modalNone
	key('F')
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	if a.modal != modalNone || len(a.rows) != 3 {
		t.Fatalf("esc cancels: modal %d rows %d", a.modal, len(a.rows))
	}
	key('F')
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	drain(t, a)
	if a.view != viewList || len(a.rows) != 2 || !strings.HasPrefix(a.toast, "Forgotten.") {
		t.Fatalf("after forget: view %d rows %d toast %q", a.view, len(a.rows), a.toast)
	}
	for _, r := range a.rows {
		if r.s.ID == s.ID {
			t.Error("the forgotten session is still listed")
		}
	}
	// A forget that fails says why and leaves the list alone.
	a.forget = func(string) error { return runForget(dir, []string{"--session", "no-such-id"}) }
	a.askForget()
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	drain(t, a)
	if !strings.HasPrefix(a.toast, "Not forgotten: nothing matched") || len(a.rows) != 2 {
		t.Errorf("failed forget: toast %q rows %d", a.toast, len(a.rows))
	}
}
