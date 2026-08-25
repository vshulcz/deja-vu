package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The duplicate check reads the file line by line. A note longer than the
// scan's buffer is not compared — that much is by design — but it used to stop
// the scan, so every note written after it was never compared either, and one
// oversized note turned the check off for the whole store (#1812).
func TestOneHugeNoteDoesNotDisableTheDuplicateCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now()

	huge := strings.Repeat("the shard limit is 64. ", 240000) // ~5.5 MB
	if err := AppendNote("proj", huge, now); err != nil {
		t.Fatal(err)
	}
	if err := AppendNote("proj", "the retry cap is three", now); err != nil {
		t.Fatal(err)
	}
	err := AppendNote("proj", "the retry cap is three", now)
	if err == nil {
		t.Fatal("a note already on file was written again after an oversized one")
	}
	if err != ErrNoteExists {
		t.Fatalf("want ErrNoteExists, got %v", err)
	}

	// The control: without the oversized note in front, the same duplicate is
	// refused — so the assertion above is not vacuous.
	plain := filepath.Join(t.TempDir(), "plain.jsonl")
	t.Setenv("DEJA_NOTES_FILE", plain)
	if err := AppendNote("proj", "the retry cap is three", now); err != nil {
		t.Fatal(err)
	}
	if err := AppendNote("proj", "the retry cap is three", now); err != ErrNoteExists {
		t.Fatalf("the control does not hold: %v", err)
	}

	// And the oversized note itself is still written, unchanged.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < len(huge) {
		t.Errorf("the oversized note was not written whole: file is %d bytes", len(body))
	}
}

// The cap is on a note's content: a line of exactly maxNoteLine bytes is still
// compared, and one byte more is skipped. Counting the newline made the
// boundary one byte tight.
func TestTheLineCapCountsTheNoteNotItsNewline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fill    int
		compare bool
	}{
		{"one under", -1, true},
		{"exactly the cap", 0, true},
		{"one over", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "notes.jsonl")
			t.Setenv("DEJA_NOTES_FILE", path)
			// A note whose JSON line lands exactly on the cap.
			body := strings.Repeat("z", 64)
			if err := AppendNote("proj", body, time.Now()); err != nil {
				t.Fatal(err)
			}
			line, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			pad := maxNoteLine - len(strings.TrimRight(string(line), "\n")) + tc.fill
			if pad < 0 {
				t.Skip("the stored line is already past the cap")
			}
			grown := strings.Repeat("z", 64+pad)
			path2 := filepath.Join(t.TempDir(), "notes2.jsonl")
			t.Setenv("DEJA_NOTES_FILE", path2)
			if err := AppendNote("proj", grown, time.Now()); err != nil {
				t.Fatal(err)
			}
			err = AppendNote("proj", grown, time.Now())
			if tc.compare && err != ErrNoteExists {
				t.Errorf("a line at the cap was not compared: %v", err)
			}
			if !tc.compare && err != nil {
				t.Errorf("a line past the cap should be written without comparison: %v", err)
			}
		})
	}
}
