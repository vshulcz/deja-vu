package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// datedStore writes sessions with identical text at the given ages, so nothing
// but the timestamp can decide how they rank.
func datedStore(t *testing.T, stamps map[string]string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-tmp-projm")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for sid, stamp := range stamps {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/tmp/projm","timestamp":"` + stamp +
			`","message":{"role":"user","content":"we reworked the invoice exporter and its retry loop"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func firstHitID(t *testing.T, out string, ids ...string) string {
	t.Helper()
	best, at := "", len(out)+1
	for _, id := range ids {
		if i := strings.Index(out, id); i >= 0 && i < at {
			best, at = id, i
		}
	}
	return best
}

// The site called month, year and relative phrases "searchable tokens" and
// "ranking hints". They are read by the relevance tier alone, so they do
// nothing for a query that matched something exactly — which is the ordinary
// case (#1094). This pins both halves: the hints work where they work, and
// stay out of the way where they do not.
func TestTimeHintsRankOnlyTheNoExactMatchFallback(t *testing.T) {
	now := time.Now()
	lastMonth := now.AddDate(0, -1, 0)
	dir := datedStore(t, map[string]string{
		// The reader's own calendar, not UTC: `last month` resolves against
		// the local clock, so a fixture dated in UTC lands in the previous
		// month whenever the two disagree — which on the first of a month
		// gives today's session the same month token the hint carries, and the
		// test cannot tell them apart (#2808 turned this up on 1 September).
		"m_old":  now.AddDate(0, -3, 0).Format("2006-01-02") + "T10:00:00Z",
		"m_last": lastMonth.Format("2006-01-02") + "T10:00:00Z",
		"m_now":  now.Format("2006-01-02") + "T10:00:00Z",
	})
	_ = dir

	// Baseline: nothing but recency.
	base, err := captureRun(t, "search", "invoice exporter")
	if err != nil {
		t.Fatal(err)
	}
	if got := firstHitID(t, base, "m_now", "m_last", "m_old"); got != "m_now" {
		t.Fatalf("baseline is not recency-ordered (%s):\n%s", got, base)
	}

	// A query with no exact match: the hint decides.
	out, err := captureRun(t, "search", "what did we do last month")
	if err != nil {
		t.Fatal(err)
	}
	if got := firstHitID(t, out, "m_now", "m_last", "m_old"); got != "m_last" {
		t.Errorf("`last month` did not rank last month's session first (got %s):\n%s", got, out)
	}

	// The same phrase on a query that already matches: the words win, and the
	// site must not promise otherwise.
	out, err = captureRun(t, "search", "invoice exporter last month")
	if err != nil {
		t.Fatal(err)
	}
	if got := firstHitID(t, out, "m_now", "m_last", "m_old"); got != "m_now" {
		t.Errorf("a time phrase re-ranked an exact-matching query (got %s) — if this is now"+
			" intended, the site copy has to say so:\n%s", got, out)
	}
}

// A bare year never reaches the tier that reads date tokens, so it answers
// "no matches" on a store where every session carries that year.
func TestBareYearFindsNothingWhileTheYearStillRanks(t *testing.T) {
	year := time.Now().AddDate(-2, 0, 0).Format("2006")
	dir := datedStore(t, map[string]string{
		"y_old": year + "-05-14T10:00:00Z",
		"y_new": time.Now().Add(-2*time.Hour).UTC().Format("2006-01-02") + "T10:00:00Z",
	})
	_ = dir

	// With content words the year selects; this is what makes it a real token.
	out, err := captureRun(t, "search", "invoice exporter "+year)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "y_old") {
		t.Errorf("the year did not reach its session alongside content words:\n%s", out)
	}

	// Alone it does not, and the site no longer claims a bare year is a query.
	out, err = captureRunStderr(t, "search", year)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no matches") {
		t.Logf("a bare year now answers — the site copy can be widened again:\n%s", out)
	}
}

// The copy has to keep naming the condition, or the measurement above stops
// matching what a reader is told.
func TestSiteSearchPageScopesTheTimeHints(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "search.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if strings.Contains(page, "as searchable tokens") {
		t.Errorf("site still calls month/year general searchable tokens; they are read by the" +
			" relevance tier only (#1094)")
	}
	if !strings.Contains(page, "no exact match") {
		t.Errorf("site no longer says when the time hints apply")
	}
}
