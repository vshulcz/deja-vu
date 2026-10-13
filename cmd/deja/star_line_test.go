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

// One sentence, once per index: the install proof says it, and after that
// neither the proof nor the week note repeats it.
func TestStarLineIsSaidOncePerIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := starLine(dir); !strings.Contains(got, "github.com/vshulcz/deja-vu") {
		t.Fatalf("first call = %q", got)
	}
	if again := starLine(dir); again != "" {
		t.Fatalf("said twice: %q", again)
	}
	// The week note after an install that already said it stays as it was.
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	now := time.Now()
	_ = weekNoteAt(dir, now)
	if got := weekNoteAt(dir, now.Add(8*24*time.Hour)); strings.Contains(got, "github.com") {
		t.Fatalf("week note repeated the star line: %q", got)
	}
}

// A package install has no proof, so the line waits in the session-start
// notes: never before the built note has been shown, once after it, and
// never alongside another note.
func TestSessionStartCarriesTheStarLineWhenTheInstallDidNot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := humanNotes(dir); got != "" {
		t.Fatalf("a machine with nothing indexed heard %q", got)
	}
	if _, err := os.Stat(dir + ".starnote"); err == nil {
		t.Fatal("the line was spent before anything was indexed")
	}
	// The built note has been shown.
	if err := os.WriteFile(dir+".builtnote", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := humanNotes(dir); got != starText {
		t.Fatalf("the session after the built note = %q, want the GitHub line alone", got)
	}
	if again := humanNotes(dir); again != "" {
		t.Fatalf("said twice: %q", again)
	}
}

// The week note no longer carries it: a due week note and the line are two
// notes, and a session start shows one.
func TestWeekNoteGoesFirstAndTheStarLineWaits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".builtnote", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	old := strconv.FormatInt(time.Now().Add(-8*24*time.Hour).Unix(), 10)
	if err := os.WriteFile(dir+".weeknote", []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := humanNotes(dir); got != "deja this week: 1 recall — deja stats --card" {
		t.Fatalf("first session start = %q, want the week note alone", got)
	}
	if got := humanNotes(dir); got != starText {
		t.Fatalf("second session start = %q, want the GitHub line", got)
	}
}
