package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/tui"
)

// typeQuery puts q in the box and waits for the search and, when it comes
// back empty, for the look beyond it.
func typeQuery(t *testing.T, a *tuiApp, q string) {
	t.Helper()
	a.query = []rune(q)
	a.reload()
	drain(t, a)
	if len(a.rows) == 0 {
		deadline := time.Now().Add(5 * time.Second)
		for a.beyond.listed != a.listed && time.Now().Before(deadline) {
			drain(t, a)
		}
	}
}

// An empty answer offers the way forward: the other projects when the query
// is only there, the spelling the history uses when a word is off.
func TestTUIEmptyOffersAWayForward(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)

	typeQuery(t, a, "in:checkout idempotency")
	s := screen(a.frame(120, 36))
	wantOnScreen(t, s, "Nothing for", "Not in checkout, but other projects have it.", "↵  1 session in other projects")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if string(a.query) != "idempotency" {
		t.Fatalf("↵ drops the in: filter, query = %q", string(a.query))
	}
	drain(t, a)
	if len(a.rows) != 1 {
		t.Errorf("rows = %d", len(a.rows))
	}

	typeQuery(t, a, "claude: signtre")
	s = screen(a.frame(120, 36))
	wantOnScreen(t, s, "Did you mean “signature”? It finds 1 session.", "Search “signature”", "Drop a word")
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if string(a.query) != "claude: signature" {
		t.Fatalf("↵ takes the spelling and keeps the filter, query = %q", string(a.query))
	}

	typeQuery(t, a, "kafka rebalance")
	s = screen(a.frame(120, 36))
	wantOnScreen(t, s, "Drop a word")
	if strings.Contains(s, "Did you mean") || strings.Contains(s, "other projects") {
		t.Errorf("nothing to offer, offered:\n%s", s)
	}
	a.handle(tui.Event{Kind: tui.EvKey, Key: tui.KeyEnter})
	if string(a.query) != "kafka rebalance" || a.view != viewList {
		t.Errorf("↵ with no offer does nothing: %q view %d", string(a.query), a.view)
	}
}

func TestDidYouMean(t *testing.T) {
	dir, _ := tuiStore(t)
	for q, want := range map[string]string{
		"signtre rotaton": "signature rotation",
		"webhook":         "",
		"kafka":           "",
		"xyz":             "",
	} {
		if got := index.DidYouMean(dir, q); got != want {
			t.Errorf("DidYouMean(%q) = %q, want %q", q, got, want)
		}
	}
}
