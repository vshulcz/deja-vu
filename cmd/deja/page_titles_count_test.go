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

	// The home page says it in a figure of its own — "Works with 18" — sitting
	// above a grid that listed twenty. The digit test looks for "N harnesses"
	// or "N agents" and a bare number in a span is neither, so it stayed at
	// eighteen through three harnesses landing.
	home, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(`Works with <span class="fig">%d</span>`, n); !strings.Contains(string(home), want) {
		t.Errorf("docs/index.html does not say %q — the registry has %d harnesses", want, n)
	}

	// The registry pages close with "deja reads this format and N others". The
	// word was typed into the generator's template, so all twenty pages kept
	// saying seventeen for three harnesses running.
	dir := filepath.Join(root, "docs", "registry")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("registry pages: %v", err)
	}
	want := fmt.Sprintf("this format and %s others", countWordForTest(n-1))
	pages := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".html" || e.Name() == "README.html" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		pages++
		if !strings.Contains(string(b), want) {
			t.Errorf("docs/registry/%s does not say %q", e.Name(), want)
		}
	}
	if pages != n {
		t.Errorf("%d registry pages for %d harnesses", pages, n)
	}
}

// countWordForTest spells the number the generator's template spells.
func countWordForTest(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven",
		"eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen", "twenty", "twenty-one", "twenty-two",
		"twenty-two", "twenty-three", "twenty-four", "twenty-five"}
	if n < 0 || n >= len(words) {
		return fmt.Sprint(n)
	}
	return words[n]
}
