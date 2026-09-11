package main

import (
	"strings"
	"testing"
)

// The line names four of the files a session touched, and Touched is kept in path
// order, so a session that worked on many files named the same four — the ones
// that sort first — to every question that reached it. Measured over sixteen
// recall calls on a real store, 1 of the 10 lines served held any word of the
// question; with this, 4.
func TestTheFilesLineNamesTheOnesAskedAbout(t *testing.T) {
	dir, s := touchedLineStore(t,
		"/w/app/internal/store/alpha.go",
		"/w/app/internal/store/beta.go",
		"/w/app/internal/store/delta.go",
		"/w/app/internal/store/gamma.go",
		"/w/app/internal/store/omega.go",
		// The file the question is about sorts last, so the four-path cut drops
		// it: Touched is kept in path order, which is what made one session
		// answer every question with the same three files.
		"/w/app/internal/store/retry_backoff.go",
	)
	got := recallTouchedLine(dir, s, []string{"backoff", "retries"})
	if !strings.Contains(got, "retry_backoff.go") {
		t.Errorf("the file the question names is missing from the line: %q", got)
	}
	// And it leads: an agent reads the first path.
	if i, j := strings.Index(got, "retry_backoff.go"), strings.Index(got, "alpha.go"); j >= 0 && i > j {
		t.Errorf("a file unrelated to the question came first: %q", got)
	}
}

// With nothing in the question to go on, the line is what it was: path order,
// untouched. Short words are no signal either — "go" and "md" would promote
// every file in a Go repo.
func TestTheFilesLineKeepsItsOrderWithoutTerms(t *testing.T) {
	paths := []string{
		"/w/app/internal/store/alpha.go",
		"/w/app/internal/store/walk.go",
		"/w/app/internal/store/stat.go",
	}
	dir, s := touchedLineStore(t, paths...)
	for _, terms := range [][]string{nil, {"the", "a", "of"}, {"go", "md"}} {
		got := recallTouchedLine(dir, s, terms)
		if i, j := strings.Index(got, "alpha.go"), strings.Index(got, "stat.go"); i < 0 || j < 0 || i > j {
			t.Errorf("terms %v reordered the line: %q", terms, got)
		}
	}
}
