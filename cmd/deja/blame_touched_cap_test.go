package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A blame row carried every file its session touched — up to the forty the
// manifest holds. Measured on a real store, that was 3186 of an 8044-byte
// answer, about files the question did not ask about, and the answer is trimmed
// by dropping whole sessions to fit its budget: six real paths came back with 20
// sessions and 9.9 KB of quoted history, and with the list bounded the same
// budget carried 38 sessions and 15.7 KB.
func TestABlameRowNamesAFewTouchedFilesNotForty(t *testing.T) {
	var touched []string
	for i := range 30 {
		touched = append(touched, fmt.Sprintf("/work/app/internal/thing%02d.go", i))
	}
	hit := search.BlameHit{
		Session: model.Session{
			Harness: "claude", ID: "s1", Project: "app",
			Title: "touched a great many files", Touched: touched,
		},
		Count: 1, Snippets: []string{"… internal/thing00.go …"},
	}
	body := renderMCPBlame("internal/thing00.go", "thing00.go", []search.BlameHit{hit}, 0, "")
	var line string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "also worked on: ") {
			line = strings.TrimPrefix(l, "also worked on: ")
		}
	}
	if line == "" {
		t.Fatalf("the row names no other file at all:\n%s", body)
	}
	got := strings.Split(line, ", ")
	if len(got) > blameTouchedCap {
		t.Errorf("the row names %d touched files, which is the manifest's list rather than a few", len(got))
	}
	// The head of the list is the most-touched end, so that is what survives,
	// past the file that was asked about.
	if got[0] != "thing01.go" {
		t.Errorf("the first other file is %q, not the one the session touched most", got[0])
	}
	if strings.Contains(line, "/work/") {
		t.Errorf("the row spends its budget on full paths: %s", line)
	}
}
