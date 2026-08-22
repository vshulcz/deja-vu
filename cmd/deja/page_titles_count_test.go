package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The third way the count is written, after the spelled-out word and the plain
// digit: "Claude Code, Codex and 15 more" — a number that means "everything
// except the ones I just named", so it goes stale on a harness landing without
// any of the words a search for the count would find. It is also the line a
// search engine shows and the one a link preview renders, on the two pages
// people arrive at.
func TestPageTitlesCountTheRestOfTheHarnesses(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	for _, c := range []struct {
		file  string
		named int    // harnesses named in the phrase itself
		shape string // %d is the rest
	}{
		{"docs/index.html", 2, "Claude Code, Codex and %d more agents"},
		{"docs/guide/harnesses.html", 3, "Claude Code, Codex, Cursor and %d more agents"},
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.file)))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		want := fmt.Sprintf(c.shape, n-c.named)
		if !strings.Contains(string(b), want) {
			t.Errorf("%s does not say %q — the registry has %d harnesses", c.file, want, n)
		}
	}
}
