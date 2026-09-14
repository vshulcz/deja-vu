package sources

import (
	"os/exec"
	"path/filepath"
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

// And the budget is on the command, not merely computed: a read given no time
// has to come back as an error rather than run to completion.
func TestASQLiteReadRunsOutOfBudget(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "store.db")
	script := "create table t(a text); insert into t values('one'),('two');"
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("building the fixture: %v: %s", err, out)
	}

	// With a budget nothing can meet.
	t.Setenv("DEJA_STORE_TIMEOUT", "1ns")
	if _, err := sqliteOutput(db, "select a from t"); err == nil {
		t.Error("a read with no time to run came back without an error")
	}

	// With the cap off, the same read answers — so the failure above is the
	// budget and not the fixture.
	t.Setenv("DEJA_STORE_TIMEOUT", "0")
	out, err := sqliteOutput(db, "select a from t")
	if err != nil {
		t.Fatalf("the same read failed with the cap off: %v", err)
	}
	if len(out) == 0 {
		t.Error("the read answered nothing with the cap off")
	}

	// The command form carries it too, which is what the fourteen call sites
	// use: a deadline on the context, released by the caller.
	t.Setenv("DEJA_STORE_TIMEOUT", "1ns")
	cmd, stop := sqliteReadCmd(db, "select a from t")
	defer stop()
	if err := cmd.Run(); err == nil {
		t.Error("the command form ran to completion with no time to do it")
	}
}
