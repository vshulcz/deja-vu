package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The search list read and ranked every session a word matched before it
// drew the first card: 16 s for a common word on a 40k-session store
// (#5487). It reads a page now, heads the list with every match all the same,
// and reads the next page as the selection nears the end, keeping the cards
// already listed where they were.
func TestTUIListReadsAPageAtATime(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(tuiPage) + 600
	dir := commonWordStore(t, n, func(int) string { return work })
	read := 0
	prev := tuiIndexSearch
	tuiIndexSearch = func(dir string, o search.Options, progress io.Writer) (index.SearchResult, error) {
		r, err := prev(dir, o, progress)
		read += len(r.Sessions)
		return r, err
	}
	t.Cleanup(func() { tuiIndexSearch = prev })

	a := newTestTUI(t, dir)
	typeQuery(t, a, "deploy")
	if len(a.rows) != tuiPage {
		t.Fatalf("listed %d cards, want a page of %d", len(a.rows), tuiPage)
	}
	if read >= n {
		t.Errorf("read %d sessions to list %d of %d", read, tuiPage, n)
	}
	if label, _ := a.sectionLabel(); !strings.HasPrefix(label, grouped(n)+" sessions") {
		t.Errorf("the list is headed %q, want every one of the %d matches", label, n)
	}
	first := make([]string, len(a.rows))
	for i, r := range a.rows {
		first[i] = sessionKey(r.s)
	}

	// Short of the end, nothing more is read.
	read = 0
	a.move(tuiPage - tuiMoreAhead - 2)
	if a.loadingMore || read != 0 {
		t.Fatalf("asked for more %d cards from the end", len(a.rows)-a.sel)
	}
	a.move(len(a.rows))
	drain(t, a)
	if len(a.rows) != 2*tuiPage {
		t.Fatalf("after scrolling to the end the list holds %d cards, want %d", len(a.rows), 2*tuiPage)
	}
	for i, k := range first {
		if sessionKey(a.rows[i].s) != k {
			t.Fatalf("card %d moved when the next page arrived", i+1)
		}
	}
	if a.sel != tuiPage-1 {
		t.Errorf("selection is on card %d, want it to stay on %d", a.sel+1, tuiPage)
	}
}

// A quoted phrase over two common words matches on the postings far more
// sessions than say it, and the pool held few of those that do: one card
// under "1,206 sessions" where five said the phrase. A page that comes back
// short while sessions were left unread is read whole.
func TestTUIShortPageIsReadWhole(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(tuiPage) + 300
	dir := storeOf(t, n, func(int) string { return work }, func(i int) string {
		if i < 5 {
			return fmt.Sprintf("alpha beta settled it, run %d", i)
		}
		return fmt.Sprintf("alpha went first and beta after, run %d", i)
	})
	hits, total, err := tuiSearchPage(dir, search.Options{Query: `"alpha beta"`, Limit: tuiPage})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 || total != 5 {
		t.Errorf("listed %d of %d, want the 5 sessions that say it", len(hits), total)
	}
}
