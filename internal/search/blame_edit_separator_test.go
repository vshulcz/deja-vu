package search

import (
	"strings"
	"testing"
)

// An edit record is "path\nspan" and the two were joined with a space, so the
// line read as one path with rubbish after it — on the command whose output
// people paste into `deja restore <path>`, and which deliberately handles paths
// containing spaces (#2284).
func TestAnEditSnippetMarksWhereThePathEnds(t *testing.T) {
	target := BlameTarget{Base: "upload.go", FullPath: "/work/app/api/upload.go"}
	got := blameSnippet("/work/app/api/upload.go\nretryBudget := 3 // three is plenty", "edit", target)

	// The premise: both halves are still there, on one line.
	if !strings.HasPrefix(got, "/work/app/api/upload.go") || !strings.Contains(got, "retryBudget") {
		t.Fatalf("the snippet lost one of its halves: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("the snippet broke into two lines: %q", got)
	}
	if strings.HasPrefix(got, "/work/app/api/upload.go retryBudget") {
		t.Errorf("nothing marks where the path ends: %q", got)
	}
	if !strings.Contains(got, "/work/app/api/upload.go — ") {
		t.Errorf("the separator is missing: %q", got)
	}

	// A path with spaces still arrives whole, which is what #2044 was about.
	spaces := BlameTarget{Base: "two  spaces.go", FullPath: "/tmp/app/two  spaces.go"}
	got = blameSnippet("/tmp/app/two  spaces.go\nsize = 20", "edit", spaces)
	if !strings.HasPrefix(got, "/tmp/app/two  spaces.go — ") {
		t.Errorf("a path holding two spaces lost them or its separator: %q", got)
	}

	// An edit with no span left is just the path, with no separator dangling.
	got = blameSnippet("/work/app/api/upload.go\n", "edit", target)
	if strings.Contains(got, "—") {
		t.Errorf("an edit with no span printed a bare separator: %q", got)
	}
}
