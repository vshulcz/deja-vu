package main

import (
	"os"
	"path/filepath"
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

// A package install has no proof, so the first week note is where the
// sentence lands — and only the first.
func TestWeekNoteCarriesTheStarLineWhenTheInstallDidNot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	now := time.Now()
	_ = weekNoteAt(dir, now)
	got := weekNoteAt(dir, now.Add(8*24*time.Hour))
	if !strings.HasPrefix(got, "deja this week: 1 recall") || !strings.Contains(got, "github.com/vshulcz/deja-vu") {
		t.Fatalf("first week note = %q", got)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	if again := weekNoteAt(dir, now.Add(16*24*time.Hour)); strings.Contains(again, "github.com") {
		t.Fatalf("second week note repeated it: %q", again)
	}
}
