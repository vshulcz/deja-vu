package search

import "testing"

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
