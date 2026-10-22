package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// sortedIDs runs a search and returns the session ids of its JSON hits.
func sortedIDs(t *testing.T, dir string, args ...string) (ids []string, total int) {
	t.Helper()
	out := captureStdout(t, func() {
		if err := runSearch(dir, append([]string{"--json"}, args...), ""); err != nil {
			t.Error(err)
		}
	})
	var env struct {
		Total int `json:"total"`
		Hits  []struct {
			Session struct {
				ID string `json:"id"`
			} `json:"session"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v\n%.400s", err, out)
	}
	for _, h := range env.Hits {
		ids = append(ids, h.Session.ID)
	}
	return ids, env.Total
}

// sessionN is the id commonWordStore gives session i; session i is i minutes
// newer than session 0.
func sessionN(i int) string { return fmt.Sprintf("%08d-2222-4000-8000-a1b2c3d4e5f6", i) }

// Results came back by relevance only, and finding last week's session among
// them meant reading every page (#5577). --sort date lists the newest match
// first, in the text and in --json.
func TestSearchSortDateListsNewestFirst(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := commonWordStore(t, 12, func(int) string { return work })

	ranked, _ := sortedIDs(t, dir, "deploy")
	byDate, total := sortedIDs(t, dir, "--sort", "date", "deploy")
	if total != 12 || len(byDate) != 12 {
		t.Fatalf("--sort date: %d hits of %d, want 12", len(byDate), total)
	}
	for i, id := range byDate {
		if want := sessionN(11 - i); id != want {
			t.Fatalf("--sort date hit %d is %s, want %s:\n%v", i+1, id, want, byDate)
		}
	}
	if strings.Join(ranked, ",") == strings.Join(byDate, ",") {
		t.Fatal("the ranked order is already newest first, so this test proves nothing")
	}
	if rel, _ := sortedIDs(t, dir, "--sort", "relevance", "deploy"); strings.Join(rel, ",") != strings.Join(ranked, ",") {
		t.Errorf("--sort relevance changed the default order:\n%v\n%v", rel, ranked)
	}
}

// A word most sessions hold is ranked from a pool of the best thousand or so
// (#5467). Sorting that pool by date listed the newest of the best, which
// skipped every new session that scored low; the date order reads the newest
// matches instead, so the page is the newest of the whole match.
func TestSearchSortDateOverACappedPool(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(search.DefaultLimit) + 600
	dir := commonWordStore(t, n, func(int) string { return work })

	ids, total := sortedIDs(t, dir, "--sort", "date", "deploy")
	if total != n {
		t.Errorf("total %d, want %d", total, n)
	}
	if len(ids) != search.DefaultLimit {
		t.Fatalf("%d hits, want a page of %d", len(ids), search.DefaultLimit)
	}
	for i, id := range ids {
		if want := sessionN(n - 1 - i); id != want {
			t.Fatalf("hit %d is %s, want %s: the page skipped newer matches\n%v", i+1, id, want, ids)
		}
	}
}

// The text answer says the order it is in.
func TestSearchSortDateSaysSo(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := commonWordStore(t, 3, func(int) string { return work })
	errOut := captureStderr(t, func() {
		captureStdout(t, func() {
			if err := runSearch(dir, []string{"--sort", "date", "deploy"}, ""); err != nil {
				t.Error(err)
			}
		})
	})
	if !strings.Contains(errOut, "newest first") {
		t.Errorf("stderr does not name the order:\n%s", errOut)
	}
}

func TestSearchSortRejectsAnUnknownOrder(t *testing.T) {
	if _, err := parseSearch([]string{"--sort", "size", "deploy"}); err == nil || !strings.Contains(err.Error(), "date or relevance") {
		t.Errorf("--sort size: %v", err)
	}
	o, err := parseSearch([]string{"--sort=date", "deploy"})
	if err != nil || o.Sort != search.SortDate || o.Query != "deploy" {
		t.Errorf("--sort=date: %+v %v", o, err)
	}
}

// ^s turns the search list newest first and back. The newest are read from
// the whole match a page at a time, so the next page carries on in date order
// rather than sorting what one page of the ranking held (#5577).
func TestTUISortToggleReadsTheNewestMatches(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work", "shopfront")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(tuiPage) + 600
	dir := commonWordStore(t, n, func(int) string { return work })
	a := newTestTUI(t, dir)
	typeQuery(t, a, "deploy")
	if label, note := a.sectionLabel(); !strings.HasSuffix(note, "best first") {
		t.Errorf("a ranked list is headed %q · %q", label, note)
	}
	if s := screen(a.frame(120, 36)); !strings.Contains(s, "^s  newest first") {
		t.Errorf("the footer does not offer the date order:\n%s", s)
	}
	// At 80 columns the footer keeps what it had; the label names the order.
	wantOnScreen(t, screen(a.frame(80, 30)), "continue in…", "best first")

	a.ctrlKey('s')
	drain(t, a)
	if !a.byDate {
		t.Fatal("^s did not turn the list to date order")
	}
	checkNewest := func(upTo int) {
		t.Helper()
		if len(a.rows) < upTo {
			t.Fatalf("listed %d cards, want %d", len(a.rows), upTo)
		}
		for i := range upTo {
			if want := sessionN(n - 1 - i); a.rows[i].s.ID != want {
				t.Fatalf("card %d is %s, want %s", i+1, a.rows[i].s.ID, want)
			}
		}
	}
	checkNewest(tuiPage)
	if a.rows[0].section == "" {
		t.Error("a date-ordered list has no day heading over its first card")
	}
	if _, note := a.sectionLabel(); !strings.Contains(note, "newest first") {
		t.Errorf("the header does not say the order: %q", note)
	}
	if label, _ := a.sectionLabel(); !strings.HasPrefix(label, grouped(n)+" sessions") {
		t.Errorf("the list is headed %q, want every one of the %d matches", label, n)
	}
	wantOnScreen(t, screen(a.frame(120, 36)), "newest first", "^s  best first")

	a.move(len(a.rows))
	drain(t, a)
	checkNewest(2 * tuiPage)

	a.ctrlKey('s')
	drain(t, a)
	if a.byDate {
		t.Fatal("^s again left the list in date order")
	}
	// It reads as far as the reader scrolled, ranked over that deeper read.
	o := a.box().options(nil)
	o.Limit = a.asked
	want, _, err := tuiSearchPage(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	for i, h := range want {
		if a.rows[i].s.ID != h.Session.ID {
			t.Fatalf("back to best first, card %d is %s, want %s", i+1, a.rows[i].s.ID, h.Session.ID)
		}
	}
	if a.rows[0].section != "" {
		t.Errorf("a ranked list carries the day heading %q", a.rows[0].section)
	}
}

// The relevance and error tiers rank a window before anything is ordered, so
// a date order over them is the newest of that window, and says so when the
// match ran past it.
func TestNewestFirstNoteNamesARankedWindow(t *testing.T) {
	for _, c := range []struct {
		o          search.Options
		shown      int
		tierWindow bool
		window     int
		want       string
	}{
		{search.Options{Total: 3}, 3, false, 0, "all 3 matches, newest first"},
		{search.Options{Total: 1600}, 15, false, 0, "newest first"},
		{search.Options{Total: 1234}, 15, true, 50, "newest first among the 50 closest of 1,234"},
		{search.Options{Total: 40}, 15, false, 40, "newest first"},
	} {
		if got := newestFirstNote(c.o, c.shown, c.tierWindow, c.window); got != c.want {
			t.Errorf("newestFirstNote(%d, %d, %v, %d) = %q, want %q", c.o.Total, c.shown, c.tierWindow, c.window, got, c.want)
		}
	}
}

// A relevance answer read by date lists the sessions holding every word
// first. The rest restart the days, so they are headed apart rather than
// under a second "Yesterday".
func TestTUIDateHeadsPartialMatchesApart(t *testing.T) {
	dir, _ := tuiStore(t)
	a := newTestTUI(t, dir)
	hits := make([]search.Hit, 0, len(a.rows))
	for i, r := range a.rows {
		hits = append(hits, search.Hit{Session: r.s, Strict: i == len(a.rows)-1})
	}
	search.ByDate(hits)
	a.rowsByDate = true
	a.setRows(nil, hits, len(hits))
	var heads []string
	for _, r := range a.rows {
		if r.section != "" {
			heads = append(heads, r.section)
		}
	}
	want := []string{"This week", "Yesterday · some of the words", "This week · some of the words"}
	if strings.Join(heads, "|") != strings.Join(want, "|") {
		t.Errorf("headings %q, want %q", heads, want)
	}
}
