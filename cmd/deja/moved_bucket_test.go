package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A note bucket's id carries the day it was minted in, so a machine that
// changes zone renames its buckets on the next build. Every id refusal then
// read as "that note is gone" while the note sat under the neighbouring day
// (#1039).
func TestAStaleBucketIdSaysTheDaysRegrouped(t *testing.T) {
	tmp := hermeticEnv(t)
	saved := time.Local
	time.Local = time.FixedZone("test+02", 2*60*60)
	t.Cleanup(func() { time.Local = saved })
	notes := filepath.Join(tmp, "notes.jsonl")
	body := `{"ts":"2026-07-16T13:40:25Z","project":"edge","text":"keep the retry budget at three"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	// East of the writer, and rebuilt: the bucket is renamed to the next day.
	time.Local = time.FixedZone("test+14", 14*60*60)
	if _, err := captureRunStderr(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	stale, moved := "deja-2026-07-16-edge", "deja-2026-07-17-edge"

	if _, err := captureRun(t, "show", stale); err == nil {
		t.Fatal("the stale id resolved")
	} else if !strings.Contains(err.Error(), moved) {
		t.Errorf("show does not name the id the note moved to: %v", err)
	}
	if _, err := captureRun(t, "promote", stale, "--state", "stale"); err == nil {
		t.Fatal("the stale id resolved for promote")
	} else if !strings.Contains(err.Error(), moved) {
		t.Errorf("promote does not name the id the note moved to: %v", err)
	}
	out, err := captureRun(t, "forget", "--session", stale)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, moved) {
		t.Errorf("forget does not name the id the note moved to:\n%s", out)
	}

	// An id that never existed keeps the plain refusal — the hint is a fact
	// about this index, not a guess.
	if _, err := captureRun(t, "show", "deja-2020-01-01-nope"); err == nil {
		t.Fatal("an invented id resolved")
	} else if strings.Contains(err.Error(), "regrouped") {
		t.Errorf("an id with no neighbour was explained anyway: %v", err)
	}
}
