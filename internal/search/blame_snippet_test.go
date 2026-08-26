package search

import (
	"strings"
	"testing"
)

// The line a blame snippet shows has to be the file that was asked about. A
// substring match printed a sibling — "mypool.go" contains "pool.go" — as the
// answer, and pasted into restore that is a different file's spans (#2044).
func TestBlameSnippetNamesTheFileThatWasAskedAbout(t *testing.T) {
	target := BlameTarget{Base: "pool.go", FullPath: "/tmp/app/pool.go"}
	for _, c := range []struct{ name, text, want string }{
		{"a sibling whose name contains it",
			"/tmp/app/mypool.go\n/tmp/app/pool.go", "/tmp/app/pool.go"},
		{"the full path beats a bare match",
			"/tmp/app/vendor/pool.go\n/tmp/app/pool.go", "/tmp/app/pool.go"},
		{"spelled in another case",
			"/tmp/app/Pool.go", "/tmp/app/Pool.go"},
		{"its own spaces kept",
			"/tmp/app/two  spaces.go\n/tmp/app/pool.go", "/tmp/app/pool.go"},
		// A store synced from Windows: on a unix host filepath sees one
		// segment, and missing the line here drops back to the renderer that
		// collapses the spaces.
		{"a windows path", `C:\src\app\pool.go`, `C:\src\app\pool.go`},
	} {
		if got := blameSnippet(c.text, "files", target); got != c.want {
			t.Errorf("%s: blameSnippet = %q, want %q", c.name, got, c.want)
		}
	}

	// An edit record is "path\nspan": the path is a path, the span is prose.
	spaces := BlameTarget{Base: "two  spaces.go", FullPath: "/tmp/app/two  spaces.go"}
	if got := blameSnippet(`C:\src\app\two  spaces.go`, "files", BlameTarget{Base: "two  spaces.go"}); got != `C:\src\app\two  spaces.go` {
		t.Errorf("a windows path lost its spaces: %q", got)
	}

	got := blameSnippet("/tmp/app/two  spaces.go\nsize = 20", "edit", spaces)
	if !strings.HasPrefix(got, "/tmp/app/two  spaces.go") {
		t.Errorf("an edit snippet lost the file's spaces: %q", got)
	}
	if !strings.Contains(got, "size = 20") {
		t.Errorf("an edit snippet lost the span: %q", got)
	}

	// Anything else is prose, and prose is what snippet() is for.
	if got := blameSnippet("we changed pool.go twice today", "user", target); got == "" {
		t.Error("a prose mention produced no snippet")
	}
}

// A path is bounded like anything else that reaches a terminal or an MCP
// payload, and bounded from the left: the tail is what names the file.
func TestSafePathIsBoundedFromTheLeft(t *testing.T) {
	long := "/tmp/" + strings.Repeat("deep/", 200) + "pool.go"
	got := SafePath(long)
	if len([]rune(got)) > pathCap {
		t.Errorf("SafePath returned %d runes, over the %d cap", len([]rune(got)), pathCap)
	}
	if !strings.HasSuffix(got, "pool.go") {
		t.Errorf("the clip dropped the file's own name: %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("a clipped path does not say it was clipped: %q", got)
	}
}

// A command is clipped from the other end than a path: what identifies it is
// the program it runs, so the head is what survives (#2052).
func TestSafeCommandIsClippedFromTheRight(t *testing.T) {
	long := "go test ./internal/index -run " + strings.Repeat("A", 400)
	got := SafeCommand(long)
	if len([]rune(got)) > pathCap {
		t.Errorf("SafeCommand returned %d runes, over the %d cap", len([]rune(got)), pathCap)
	}
	if !strings.HasPrefix(got, "go test ./internal/index -run ") {
		t.Errorf("the clip dropped the command itself: %q", got[:40])
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a clipped command does not say it was clipped: %q", got[len(got)-10:])
	}
	// A path keeps the other end, and both stay under the cap.
	if p := SafePath("/tmp/" + strings.Repeat("deep/", 200) + "pool.go"); !strings.HasSuffix(p, "pool.go") {
		t.Errorf("a path lost its own name: %q", p)
	}
}

// A note title ends in the state the note is in, and that suffix is what every
// one-line surface reads it for — so the clip keeps it. Only a short one: a long
// bracketed tail would otherwise carry the whole title past the bound (#2058).
func TestSafeNoteTitleKeepsTheStateAndStaysBounded(t *testing.T) {
	long := strings.Repeat("very long title ", 40)
	for _, c := range []struct {
		name, in string
		suffix   string
		bounded  bool
	}{
		{"a state survives the clip", long + " [rejected]", " [rejected]", true},
		{"a short title is untouched", "pool sizing [accepted]", " [accepted]", false},
		{"a long bracketed tail is not a state", "note [" + strings.Repeat("x", 400) + "]", "", true},
		{"no state at all", long, "", true},
	} {
		got := SafeNoteTitle(c.in)
		if c.suffix != "" && !strings.HasSuffix(got, c.suffix) {
			t.Errorf("%s: %q does not end in %q", c.name, got, c.suffix)
		}
		if c.bounded && len(got) > answerCap+noteStateCap {
			t.Errorf("%s: %d bytes, over the %d bound", c.name, len(got), answerCap+noteStateCap)
		}
		if strings.Contains(got, "\n") {
			t.Errorf("%s: the row is not one line: %q", c.name, got)
		}
	}
}
