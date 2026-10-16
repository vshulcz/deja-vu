package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// The Memory tab shows what Claude wrote in its own memory for the project,
// a rule above a fact, with the session that wrote it; x keeps a line from
// being handed on and y puts it back, and neither touches Claude's file.
func TestTUIMemoryShowsWhatClaudeLearned(t *testing.T) {
	dir, root := tuiStore(t)
	a := newTestTUI(t, dir)
	a.cwd, a.projects = "/work/payments", []string{"payments"}
	mem := filepath.Join(root, sources.ClaudeEncodeProject(a.cwd), "memory")
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	rule := filepath.Join(mem, "keys.md")
	files := map[string]string{
		rule:                           "---\nname: keys\ndescription: \"idempotency keys come from the charge id\"\nmetadata:\n  type: feedback\n  originSessionId: b2222222-retry\n  modified: 2026-03-02T10:00:00Z\n---\nTwo retries reused a request key.\n",
		filepath.Join(mem, "stage.md"): "---\nname: stage\ndescription: \"staging is pg-two\"\nmetadata:\n  type: project\n  modified: 2026-03-03T10:00:00Z\n---\npg-two\n",
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.openMemory()
	drain(t, a)
	for _, w := range []int{80, 120} {
		s := screen(a.frame(w, 30))
		wantOnScreen(t, s, "Memory", "LEARNED BY CLAUDE CODE", "idempotency keys come from the charge id", "staging is pg-two")
		if strings.Index(s, "idempotency keys") > strings.Index(s, "staging is pg-two") {
			t.Errorf("at %d columns a fact sits above a rule:\n%s", w, s)
		}
	}
	s := screen(a.frame(120, 30))
	wantOnScreen(t, s, "Claude Code wrote down 2 things here", "WRITTEN IN", "the retry double charges the card")

	before, _ := os.ReadFile(rule)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'x'})
	if !memHidden(dir)[rule] {
		t.Fatal("x did not set the line aside")
	}
	wantOnScreen(t, screen(a.frame(120, 30)), "NOT HANDED ON", "Claude Code wrote down 1 thing here")
	if after, _ := os.ReadFile(rule); string(after) != string(before) {
		t.Error("setting a line aside rewrote Claude's own memory file")
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'y'})
	if memHidden(dir)[rule] {
		t.Error("y did not put the line back")
	}

	// Enter opens the session that wrote it, and esc comes back here.
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if a.view != viewReader || a.reader.s.ID != "b2222222-retry" {
		t.Fatalf("enter opened view %d on %q", a.view, a.reader.s.ID)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEsc})
	if a.view != viewMemory {
		t.Errorf("esc from the source went to view %d, not back to Memory", a.view)
	}
}

// Tab walks the three scopes and then the Memory tab; typing on Memory
// starts a search the way it does everywhere else.
func TestTUIMemoryTabIsOneMoreTab(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	a.scope = scopeKept
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyTab})
	if a.view != viewMemory {
		t.Fatalf("tab from Deleted went to view %d", a.view)
	}
	drain(t, a)
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyRune, Rune: 'w'})
	if a.view != viewList || string(a.query) != "w" {
		t.Errorf("typing on Memory: view %d, query %q", a.view, string(a.query))
	}
}
