package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// `deja wip` printed `was.`, `it`, `as`, `exactly` beside the real paths: every
// whitespace-separated token of a files record counted as a file, so a record
// carrying a sentence contributed one per word — and crowded the real paths out
// of the handful that get named (#3706).
func TestFilesInFlightKeepsPathsAndDropsProse(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: sources.RoleFiles, Text: "/Users/x/work/cmd/deja/how.go"},
		{Role: sources.RoleFiles, Text: "the state as it was. exactly it"},
		{Role: sources.RoleEdit, Text: "cmd/deja/mcp.go internal/digest/resume.go"},
		{Role: sources.RoleFiles, Text: "~/.config/deja/wiring.json README.md"},
	}}
	got := ResumeFrom(s, nil).Files
	joined := strings.Join(got, " ")
	for _, want := range []string{"how.go", "mcp.go", "resume.go", "wiring.json", "README.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("a real path was dropped: %q missing from %v", want, got)
		}
	}
	for _, prose := range []string{"the", "state", "as", "it", "was.", "exactly"} {
		for _, f := range got {
			if f == prose {
				t.Errorf("prose was named as a file: %q in %v", prose, got)
			}
		}
	}
}

// The predicate itself, since it decides what an agent is told to open.
func TestLooksLikeAPath(t *testing.T) {
	for _, s := range []string{
		"/abs/path/file.go", "cmd/deja/how.go", `windows\path\file.txt`,
		"~/.config/deja/wiring.json", ".env", "README.md", "main.go",
	} {
		if !looksLikeAPath(s) {
			t.Errorf("%q is a path and was not taken", s)
		}
	}
	for _, s := range []string{
		"", "the", "state", "was.", "exactly", "it", "sentence,", "deja-vu",
		"word.longerthanextension",
	} {
		if looksLikeAPath(s) {
			t.Errorf("%q is prose and was taken as a path", s)
		}
	}
}
