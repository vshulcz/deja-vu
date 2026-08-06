package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A day of notes is one session whose id *is* a date. Reading its timestamp in
// the reader's zone put a different day on the line than the id sitting beside
// it, and an id rebuilt from what the reader saw matched nothing (#883). The
// day both carry is the reader's, so the note sits in the same calendar as the
// sessions listed around it (#911).
func TestANoteBucketShowsTheDayItsIdNames(t *testing.T) {
	tmp := hermeticEnv(t)
	// time.Local is resolved once at process start, so TZ= in the environment
	// does not move it. The bug only shows outside UTC, and CI runs in UTC —
	// so the zone is set on the package's clock and restored after.
	savedLocal := time.Local
	time.Local = time.FixedZone("test+03", 3*60*60)
	t.Cleanup(func() { time.Local = savedLocal })
	notes := filepath.Join(tmp, "notes.jsonl")
	body := `{"ts":"2026-07-16T21:01:00Z","project":"edge","text":"note just after midnight in Moscow"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deja-2026-07-17-edge") {
		t.Fatalf("bucket id changed: %q", out)
	}
	// The date on the line is the date in the id, not the reader's rendering
	// of a moment inside it.
	if !strings.Contains(out, "2026-07-17 · deja-2026-07-17-edge") {
		t.Errorf("line and id disagree:\n%s", out)
	}

	// A transcript is still shown in the reader's zone: the rule is about
	// deja's own day buckets, not about every session (#849).
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Its id even looks like a bucket id: the rule keys on the harness, not on
	// the shape of the string.
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-16T21:01:00Z","sessionId":"deja-2026-07-16-lookalike","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2026-07-17 · deja-2026-07-16-lookalike") {
		t.Errorf("a transcript stopped following the reader's zone:\n%s", out)
	}
}

// Borrowing the id's day put an older date on the newest row: read from far
// enough east of the machine that minted the bucket, the column ran 06, 07, 04
// down the screen (#1038). The id is printed whole on the same line, so the
// date column can be the reader's calendar like every other row.
func TestTheDateColumnRunsOneWayDownTheScreen(t *testing.T) {
	tmp := hermeticEnv(t)
	savedLocal := time.Local
	// Minted west of the reader: the bucket id takes the writing machine's
	// day, and the reader is far enough east to be on the next one.
	time.Local = time.FixedZone("test+02", 2*60*60)
	t.Cleanup(func() { time.Local = savedLocal })
	notes := filepath.Join(tmp, "notes.jsonl")
	// A day bucket minted west of the reader, and a promoted note written a
	// moment later — the two rows that disagreed.
	// The bucket is the newest row: that is where borrowing its id's day put
	// an older date above a newer one.
	body := `{"kind":"promoted","ts":"2026-07-16T13:40:25Z","project":"edge","session":"claude:s1","state":"accepted","title":"the pooling decision","text":"decided to cap the pool"}` + "\n" +
		`{"ts":"2026-07-16T13:40:26Z","project":"edge","text":"a hand note written now"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	time.Local = time.FixedZone("test+14", 14*60*60)
	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	dates := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(line, " · ")
		if len(parts) < 3 {
			continue
		}
		dates = append(dates, parts[2])
	}
	if len(dates) < 2 {
		t.Fatalf("expected at least two rows to compare:\n%s", out)
	}
	for i := 1; i < len(dates); i++ {
		if dates[i] > dates[i-1] {
			t.Errorf("row %d is dated after the row above it (%s then %s):\n%s", i, dates[i-1], dates[i], out)
		}
	}
}
