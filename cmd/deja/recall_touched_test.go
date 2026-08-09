package main

import (
	"strings"
	"testing"
)

func TestCommonDirPrefixCollapsesTheSharedRoot(t *testing.T) {
	paths := []string{
		"/repo/internal/search/search.go",
		"/repo/internal/sources/notes.go",
		"/repo/scripts/lme/main.go",
	}
	if got := commonDirPrefix(paths); got != "/repo/" {
		t.Fatalf("commonDirPrefix = %q, want /repo/", got)
	}
}

func TestCommonDirPrefixEmptyWhenNothingWorthCollapsing(t *testing.T) {
	// One path names no root; divergent roots share nothing usable.
	if got := commonDirPrefix([]string{"/repo/a.go"}); got != "" {
		t.Errorf("single path returned %q", got)
	}
	if got := commonDirPrefix([]string{"/one/a.go", "/two/b.go"}); got != "" {
		t.Errorf("divergent roots returned %q", got)
	}
	if got := commonDirPrefix(nil); got != "" {
		t.Errorf("nil returned %q", got)
	}
}

// A shared prefix must not be cut mid-name: /repo/alpha and /repo/alphabet
// share the bytes "/repo/alpha" but only the directory /repo/.
func TestCommonDirPrefixStopsAtADirectoryBoundary(t *testing.T) {
	got := commonDirPrefix([]string{"/repo/alpha/x.go", "/repo/alphabet/y.go"})
	if got != "/repo/" {
		t.Fatalf("commonDirPrefix = %q, want /repo/", got)
	}
	if strings.Contains(got, "alpha") {
		t.Errorf("prefix cut mid-directory: %q", got)
	}
}
