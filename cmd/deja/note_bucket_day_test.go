package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A day of notes is one session whose id *is* a date, minted in UTC. Reading
// its timestamp in the reader's zone put a different day on the line than the
// id sitting beside it, and an id rebuilt from what the reader saw matched
// nothing (#883).
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
	if !strings.Contains(out, "deja-2026-07-16-edge") {
		t.Fatalf("bucket id changed: %q", out)
	}
	// The date on the line is the date in the id, not the reader's rendering
	// of a moment inside it.
	if !strings.Contains(out, "2026-07-16 · deja-2026-07-16-edge") {
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
