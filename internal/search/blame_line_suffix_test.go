package search

import (
	"strings"
	"testing"
)

// What a reader has in the clipboard is `file:line` — from a stack trace, a
// compiler error, a linter, an editor's copy-reference, or deja's own lines.
// Blame took it literally, made `search.go:266` the basename, and reported that
// no session mentions the file (#1625).
func TestResolveBlamePathDropsALineNumber(t *testing.T) {
	for _, in := range []string{
		"internal/search/search.go:266",
		"internal/search/search.go:266:12",
		"/tmp/api/internal/search/search.go:266",
		"search.go:1",
	} {
		got, err := ResolveBlamePath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got.Base != "search.go" || got.Stem != "search" {
			t.Errorf("%s resolved to base %q stem %q, want search.go/search", in, got.Base, got.Stem)
		}
	}
	// Extensionless names are the common case for this shape: make, docker and
	// just all report `Makefile:12`.
	for _, in := range []string{"Makefile:12", "Dockerfile:7", "justfile:3:9"} {
		got, err := ResolveBlamePath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if strings.ContainsRune(got.Base, ':') {
			t.Errorf("%s kept its line number: base %q", in, got.Base)
		}
	}
	// The controls: a plain path is untouched, and a colon that is part of the
	// name — or a Windows drive letter — must survive.
	for in, want := range map[string]string{
		"internal/search/search.go": "search.go",
		"weird:name.go":             "weird:name.go",
		"notes:2026.md":             "notes:2026.md",
		"search.go:":                "search.go:",
	} {
		got, err := ResolveBlamePath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got.Base != want {
			t.Errorf("%s resolved to base %q, want %q", in, got.Base, want)
		}
	}
}

// A drive letter and a UNC prefix carry colons and backslashes that must reach
// the matcher intact, whichever platform the binary was built for (#1625).
func TestResolveBlamePathKeepsWindowsShapes(t *testing.T) {
	for in, want := range map[string]string{
		`C:\src\main.go:266`:     "main.go",
		`C:\src\main.go`:         "main.go",
		`\\server\share\a.go:12`: "a.go",
	} {
		got, err := ResolveBlamePath(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		// On unix these are one path element; what matters either way is that
		// the drive letter is not eaten and the line number is.
		if strings.Contains(got.Base, ":266") || strings.Contains(got.Base, ":12") {
			t.Errorf("%s kept its line number: base %q", in, got.Base)
		}
		if !strings.Contains(got.Base, want) {
			t.Errorf("%s lost the file name: base %q, want it to contain %q", in, got.Base, want)
		}
	}
}

// The collision this fix accepts, written down so it is a decision and not a
// surprise: a file literally named `a:1` is read as `a` line 1. Nothing tells
// the two apart from the string alone, and the shape that reaches this command
// is overwhelmingly `Makefile:12`, not a file whose name ends in a colon and a
// digit.
func TestALiteralNameEndingInColonDigitsIsReadAsALineNumber(t *testing.T) {
	got, err := ResolveBlamePath("a:1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "a" {
		t.Errorf("base %q; the documented trade-off is that this reads as a line number", got.Base)
	}
}
