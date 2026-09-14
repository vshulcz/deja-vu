package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An excluded store is named and not read. `deja sources` drives most of its
// rows from one table and honoured the exclusion there, while aider's row and
// opencode's are written by hand below it — so the store the reporter of #3499
// actually named kept being opened, and kept printing the sqlite3 error the
// exclusion exists to silence.
func TestSourcesNamesAnExcludedStoreWithoutReadingIt(t *testing.T) {
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "opencode.db")
	// Not a database: reading it is what produces the line this test is about.
	if err := os.WriteFile(db, []byte("not a database at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)
	history := filepath.Join(os.Getenv("HOME"), ".aider.chat.history.md")
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history, []byte("# aider chat started at 2026-09-01\n\n#### hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	row := func(name string) string {
		out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, name+"\t") {
				return l
			}
		}
		t.Fatalf("no %s row in:\n%s", name, out)
		return ""
	}

	t.Setenv("DEJA_EXCLUDE_HARNESSES", "")
	if got := row("opencode"); !strings.Contains(got, "sessions=") {
		t.Fatalf("an unexcluded store does not report its counts: %q", got)
	}

	t.Setenv("DEJA_EXCLUDE_HARNESSES", "opencode,aider")
	for _, name := range []string{"opencode", "aider"} {
		got := row(name)
		if !strings.Contains(got, "excluded") {
			t.Errorf("%s is excluded and its row does not say so: %q", name, got)
		}
		if strings.Contains(got, "sessions=") {
			t.Errorf("%s is excluded and was read anyway: %q", name, got)
		}
		if strings.Contains(got, "cannot be read") {
			t.Errorf("%s is excluded and still reports why it could not be read: %q", name, got)
		}
	}
}
