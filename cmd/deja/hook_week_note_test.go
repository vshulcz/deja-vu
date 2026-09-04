package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/usage"
)

func TestWeekNoteStartsTheClockThenSpeaksOnceAWeek(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	usage.RecordResult(dir, usage.KindHook, 400, 1, false)
	now := time.Now()
	// The first session after install starts the clock and says nothing, or
	// the note would count the hook that just ran.
	if got := weekNoteAt(dir, now); got != "" {
		t.Fatalf("first session spoke: %q", got)
	}
	if got := weekNoteAt(dir, now.Add(3*24*time.Hour)); got != "" {
		t.Fatalf("mid-week spoke: %q", got)
	}
	got := weekNoteAt(dir, now.Add(8*24*time.Hour))
	if !strings.HasPrefix(got, "deja this week: 2 recalls") || !strings.Contains(got, "deja stats --card") {
		t.Fatalf("week note = %q", got)
	}
	if again := weekNoteAt(dir, now.Add(8*24*time.Hour+time.Hour)); again != "" {
		t.Fatalf("spoke twice in a week: %q", again)
	}
}

func TestWeekNoteKeepsQuietOnAnEmptyWeek(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.WriteFile(dir+".weeknote", []byte(strconv.FormatInt(past.Unix(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := weekNoteAt(dir, time.Now()); got != "" {
		t.Fatalf("an empty week was reported: %q", got)
	}
}
