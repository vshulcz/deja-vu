package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// AiderSessionAt has to name the session the way the parser does, or the live
// recall asks as a session the index does not hold.
func TestAiderSessionAtMatchesTheParser(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".aider.chat.history.md")
	first := "\n# aider chat started at 2026-10-01 09:00:00\n\n#### first  \n\nok\n\n" +
		// A header inside a fence is text, not a session.
		"```\n# aider chat started at 2026-10-01 09:30:00\n```\n"
	// Two launches in one second: the second id carries a suffix.
	second := "\n# aider chat started at 2026-10-02 10:00:00\n\n#### second  \n\nok\n"
	third := "\n# aider chat started at 2026-10-02 10:00:00\n\n#### third  \n\nok\n"
	if err := os.WriteFile(path, []byte(first+second+third), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil || len(ss) != 3 {
		t.Fatalf("parsed %d sessions (%v)", len(ss), err)
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	oct1, oct2 := at("2026-10-01 08:59:59"), at("2026-10-02 10:00:00")
	if got := AiderSessionAt(path, 0, oct1); got != ss[0].ID {
		t.Errorf("first: %q, the parser says %q", got, ss[0].ID)
	}
	if got := AiderSessionAt(path, int64(len(first+second)), oct2); got != ss[2].ID {
		t.Errorf("same-second second launch: %q, the parser says %q", got, ss[2].ID)
	}
	// Two aiders started together, both before either wrote its header:
	// nothing says which header is whose.
	if got := AiderSessionAt(path, int64(len(first)), oct2); got != "" {
		t.Errorf("two headers from the launch second, got %q", got)
	}
	// A header from long after the launch is another aider's.
	if got := AiderSessionAt(path, 0, oct1.Add(-time.Hour)); got != "" {
		t.Errorf("a header an hour after launch was taken, got %q", got)
	}
	if got := AiderSessionAt(path, int64(len(first+second+third)), oct2); got != "" {
		t.Errorf("no header past the end yet, got %q", got)
	}
	// Replaced by a shorter file: its last session is the one being written.
	if got := AiderSessionAt(path, 1<<20, oct2); got != ss[2].ID {
		t.Errorf("replaced file: %q, want %q", got, ss[2].ID)
	}
	if got := AiderSessionAt(filepath.Join(t.TempDir(), "none.md"), 0, oct1); got != "" {
		t.Errorf("missing file: %q", got)
	}
}
