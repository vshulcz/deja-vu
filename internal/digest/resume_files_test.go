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

// The five slots went to whatever was touched last, and on a real store that
// was a probe script under /tmp in 238 of 400 handovers — with a repository
// file that had lost its slot to one in 29 of them (#3716).
func TestFilesInFlightNamesTheWorkNotTheScratch(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: sources.RoleEdit, Text: "/Users/x/work/internal/index/retrieval.go"},
		{Role: sources.RoleFiles, Text: "/private/tmp/deja-probe/run.sh /tmp/scan.py"},
		{Role: sources.RoleFiles, Text: "/Users/x/.claude/projects/proj/scratchpad/notes.md"},
		{Role: sources.RoleFiles, Text: "/Users/x/work/node_modules/left-pad/index.js"},
		{Role: sources.RoleFiles, Text: "/Users/x/work/out/build.log /var/folders/t/T/probe.go"},
		// Recorded relative to a directory above it, which is how a harness
		// that stores paths as typed writes a probe script.
		{Role: sources.RoleFiles, Text: "tmp/deja-probe/shot.txt"},
	}}
	got := ResumeFrom(s, nil).Files
	if len(got) != 1 || !strings.HasSuffix(got[0], "internal/index/retrieval.go") {
		t.Fatalf("the handover should name the work and nothing else, got %v", got)
	}
	// And a session that touched only throwaway files says nothing about files
	// rather than sending the next turn to a probe script.
	only := model.Session{Messages: []model.Message{
		{Role: sources.RoleFiles, Text: "/private/tmp/deja-probe/run.sh"},
	}}
	if got := ResumeFrom(only, nil).Files; len(got) != 0 {
		t.Fatalf("got %v, want nothing to name", got)
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
