package main

import (
	"strings"
	"testing"
)

// An empty view page — nothing indexed, nothing typed — said `nothing matches
// — try deja "" for full-text search`, a search for nothing. The hint is for a
// query that matched nothing; an empty store says that instead.
func TestViewEmptyPageDoesNotSuggestAnEmptySearch(t *testing.T) {
	i := strings.Index(viewSource, `nothing matches — try deja "'+esc(q)+'"`)
	if i < 0 {
		t.Fatal("the no-match hint is gone from the page")
	}
	if !strings.HasSuffix(viewSource[:i], `:q?'<div class="empty">`) {
		t.Fatalf("the no-match hint is not guarded by a typed query:\n%s", viewSource[max(0, i-80):i])
	}
	if !strings.Contains(viewSource, "no sessions indexed yet") {
		t.Fatal("the empty store has no line of its own")
	}
}
