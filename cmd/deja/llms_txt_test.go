package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// llms.txt is the file an agent fetches instead of crawling the site, so a dead
// link in it is worse than a dead link on a page: nothing renders, nobody sees a
// 404, and the agent simply answers from whatever else it found. The spec is
// small enough to pin whole — https://llmstxt.org
func TestLLMsTxtIsWellFormedAndResolves(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "llms.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	lines := strings.Split(text, "\n")

	if !strings.HasPrefix(lines[0], "# ") {
		t.Errorf("first line is %q; the spec requires an H1 with the project name", lines[0])
	}
	summary := false
	for _, l := range lines[:min(6, len(lines))] {
		if strings.HasPrefix(l, "> ") {
			summary = true
		}
	}
	if !summary {
		t.Error("no blockquote summary near the top; that is the part an agent reads first")
	}

	links := regexp.MustCompile(`\[[^\]]+\]\((https://[^)]+)\)`).FindAllStringSubmatch(text, -1)
	if len(links) < 10 {
		t.Fatalf("only %d links; a map of the site that maps almost nothing is not worth publishing", len(links))
	}
	const site = "https://vshulcz.github.io/deja-vu/"
	for _, m := range links {
		url := m[1]
		if !strings.HasPrefix(url, site) {
			continue // github.com and other external targets are not ours to verify here
		}
		rel := strings.TrimPrefix(url, site)
		if _, err := os.Stat(filepath.Join("..", "..", "docs", rel)); err != nil {
			t.Errorf("llms.txt points at %s, which this repository does not publish", rel)
		}
	}
}
