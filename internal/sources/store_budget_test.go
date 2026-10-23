package sources

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// #3553 hung for 13m54s inside a single sqlite3 child with 0.75s of CPU in deja
// itself, and nothing in the tree set a deadline — `grep -rn
// 'context.WithTimeout|CommandContext'` found none. Every read against a SQLite
// store now carries one (#3555).
func TestStoreReadBudget(t *testing.T) {
	for _, c := range []struct {
		set  string
		want time.Duration
	}{
		{"", 10 * time.Minute},
		{"90s", 90 * time.Second},
		{"2m", 2 * time.Minute},
		// Off, for someone who would rather wait than lose a store.
		{"0", 0},
		{"-5s", 0},
		// Garbage falls back rather than turning the cap off: a typo in an
		// environment variable must not quietly remove the bound.
		{"soon", 10 * time.Minute},
		{"10", 10 * time.Minute},
	} {
		t.Setenv("DEJA_STORE_TIMEOUT", c.set)
		if got := storeReadBudget(); got != c.want {
			t.Errorf("DEJA_STORE_TIMEOUT=%q gives %v, want %v", c.set, got, c.want)
		}
	}
}

// A fast read is not cut by a budget shorter than it, since the bound is on
// standing still rather than on the clock, and the lines `.progress` adds to
// keep a long query moving never reach the caller (#5517).
func TestASQLiteReadUnderTheBudgetAnswersWithoutProgressLines(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	ResetSQLite3Probe()
	if !sqlite3Progress() {
		t.Skip("this sqlite3 has no .progress")
	}
	db := filepath.Join(t.TempDir(), "store.db")
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, "create table t(a text); insert into t values('one');").CombinedOutput(); err != nil {
		t.Fatalf("building the fixture: %v: %s", err, out)
	}
	t.Setenv("DEJA_STORE_TIMEOUT", "250ms")
	// No row until the end, and long past the budget: the progress lines
	// are all that moves.
	q := "with recursive c(x) as (select 1 union all select x+1 from c where x<6000000) select count(*) from c;"
	started := time.Now()
	out, err := sqliteOutput(db, q)
	if err != nil {
		t.Fatalf("a read that kept moving failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
	}
	if got := strings.TrimSpace(string(out)); got != "6000000" {
		t.Errorf("answer = %q, want 6000000", clip(got))
	}
	if time.Since(started) < 250*time.Millisecond {
		t.Log("the query beat the budget here, so only the filter was checked")
	}
	cmd, stop := sqliteReadCmd(db, "select a from t")
	defer stop()
	if !slices.Contains(cmd.Args, sqliteProgressArg) {
		t.Errorf("the read does not ask for progress: %q", cmd.Args)
	}
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:60] + "…" + s[len(s)-60:]
	}
	return s
}
