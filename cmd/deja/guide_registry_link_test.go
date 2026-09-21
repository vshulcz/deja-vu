package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A per-agent guide says how to wire that harness; the registry entry says what
// its transcript looks like and when it was last checked against the live
// client. Thirty-one of the thirty-four linked theirs and three did not — the
// three oldest, written before the convention — so a reader of those pages had
// no way to the format page at all.
func TestEveryPerAgentGuideLinksItsRegistryEntry(t *testing.T) {
	guides := filepath.Join("..", "..", "docs", "guide")
	entries := filepath.Join("..", "..", "docs", "registry")
	pages, err := filepath.Glob(filepath.Join(guides, "memory-for-*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 30 {
		t.Fatalf("found %d per-agent guides — the glob is wrong", len(pages))
	}
	link := regexp.MustCompile(`href="\.\./registry/([a-z0-9-]+)\.html"`)
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var linked []string
		for _, m := range link.FindAllStringSubmatch(string(b), -1) {
			if m[1] != "README" {
				linked = append(linked, m[1])
			}
		}
		if len(linked) == 0 {
			t.Errorf("%s links no registry entry — a reader there cannot reach the format page",
				filepath.Base(p))
			continue
		}
		// And it has to be an entry that exists: the filenames are display
		// slugs rather than harness ids (`claude-code.html`, `deepseek.html`),
		// so a link written from the id would 404.
		for _, id := range linked {
			if _, err := os.Stat(filepath.Join(entries, id+".html")); err != nil {
				t.Errorf("%s links registry/%s.html, which is not there", filepath.Base(p), id)
			}
		}
	}
}
