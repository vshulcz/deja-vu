package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A note bucket's id names the local day of whichever process indexed the
// line, so a `remember` under TZ=UTC and one under the machine's own zone put
// the same moment into two buckets. `show` tells the reader that `deja index`
// regroups them, and it answered that the index was up to date (#1058).
func TestIndexRegroupsNotesGroupedInAnotherZone(t *testing.T) {
	east, err := time.LoadLocation("Pacific/Kiritimati") // UTC+14
	if err != nil {
		t.Skipf("zone unavailable: %v", err)
	}
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	dir := filepath.Join(tmp, "idx")

	append := func(line string) {
		t.Helper()
		f, err := os.OpenFile(notes, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	inZone := func(zone *time.Location, f func()) {
		saved := time.Local
		time.Local = zone
		defer func() { time.Local = saved }()
		f()
	}

	// Two days apart in UTC, one day in UTC+14: the index built here splits
	// them, and carrying the laptop east regroups them.
	append(`{"ts":"2026-07-20T23:45:00Z","project":"tz","text":"the anemometer drifted"}`)
	append(`{"ts":"2026-07-21T09:00:00Z","project":"tz","text":"the barometer drifted too"}`)
	inZone(time.UTC, func() {
		if err := index.Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	})

	// The split is the premise, not the finding: without two buckets here the
	// rest of the case proves nothing.
	if _, n := index.UpToDate(dir, ""); n != 2 {
		t.Fatalf("two notes on two UTC days gave %d sessions, want the 2 this case is about", n)
	}

	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	inZone(east, func() { err = cmdIndex(dir, nil) })
	os.Stderr = stderr
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	var n int
	inZone(east, func() { _, n = index.UpToDate(dir, "") })
	if n != 1 {
		t.Errorf("deja index left %d note buckets for one day, want 1 — it said:\n%s", n, out)
	}
	if !strings.Contains(string(out), "another time zone") {
		t.Errorf("deja index regrouped without saying why:\n%s", out)
	}
}
