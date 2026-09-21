package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The day-zero table asks a reader to trust six rows of numbers about other
// people's software, so each of those tools names the repository that was
// measured (#3839). Several of the names are taken more than once on GitHub —
// there is more than one "ctx" and more than one "funes" — and the row is only
// checkable if it says which one.
func TestDayZeroLinksEveryToolItCompares(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "guide", "day-zero.html")
	page, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	row := regexp.MustCompile(`(?s)<tr><th></th><th class="us">deja-vu</th>(.*?)</tr>`).FindSubmatch(page)
	if row == nil {
		t.Fatalf("no comparison-table header row in %s", path)
	}
	cells := regexp.MustCompile(`(?s)<th[^>]*>(.*?)</th>`).FindAllSubmatch(row[1], -1)
	if len(cells) < 6 {
		t.Fatalf("header names %d tools besides deja-vu, want at least 6", len(cells))
	}

	href := regexp.MustCompile(`<a href="(https://github\.com/[^"]+)"[^>]*>([^<]+)</a>`)
	for _, cell := range cells {
		got := href.FindSubmatch(cell[1])
		if got == nil {
			t.Errorf("column %q does not link a github repository", strings.TrimSpace(string(cell[1])))
			continue
		}
		if strings.Count(strings.TrimPrefix(string(got[1]), "https://github.com/"), "/") != 1 {
			t.Errorf("%s is not an owner/repo url", got[1])
		}
	}
}
