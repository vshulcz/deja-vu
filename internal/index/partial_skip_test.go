package index

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A store can be half-readable: cursor keeps CLI transcripts as JSONL and its
// IDE sessions in SQLite, so without the sqlite3 CLI one half arrives and the
// other does not. The run narrated what it got and said nothing about the rest,
// while the same run names a store it could not read at all (#1758).
func TestNarrationNamesAHalfReadStore(t *testing.T) {
	got := harnessNarration("cursor", []model.Session{{ID: "a", Messages: []model.Message{{Text: "x"}}}}, "sqlite3 CLI not found")
	if !strings.Contains(got, "1 session") {
		t.Errorf("the line lost its count: %q", got)
	}
	if !strings.Contains(got, "sqlite3 CLI not found") {
		t.Errorf("a half-read store says nothing about the half it could not read: %q", got)
	}

	// Nothing skipped: the line is what it always was.
	plain := harnessNarration("claude", []model.Session{{ID: "a", Messages: []model.Message{{Text: "x"}}}}, "")
	if strings.Contains(plain, "—") {
		t.Errorf("a fully read store gained a caveat: %q", plain)
	}

	// The notes pseudo-source keeps its label.
	if n := harnessNarration("deja", []model.Session{{ID: "a", Messages: []model.Message{{Text: "x"}}}}, ""); !strings.HasPrefix(n, "deja: notes:") {
		t.Errorf("notes narrate as notes: %q", n)
	}
}
