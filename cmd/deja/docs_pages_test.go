package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The guide pages share one sidebar, one head shape and one sitemap. Each is
// hand-written HTML, so the way they drift is silent: a page that is not in the
// sitemap is a page search engines never fetch, a page whose sidebar forgets a
// sibling is a page readers cannot leave, and a page marking two entries as
// current is one nobody notices until it looks wrong in a screenshot.
func TestGuidePagesShareTheirNavigation(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "docs", "guide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var pages []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".html") {
			pages = append(pages, e.Name())
		}
	}
	if len(pages) < 8 {
		t.Fatalf("only %d guide pages found; the glob is wrong", len(pages))
	}

	sitemap, err := os.ReadFile(filepath.Join(root, "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}

	link := regexp.MustCompile(`href="([a-z0-9-]+\.html)"`)
	known := map[string]bool{}
	for _, p := range pages {
		known[p] = true
	}

	for _, name := range pages {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		page := string(b)

		if n := strings.Count(page, `aria-current="page"`); n != 1 {
			t.Errorf("%s marks %d sidebar entries as current, want exactly one", name, n)
		}
		if !strings.Contains(string(sitemap), "guide/"+name) {
			t.Errorf("%s is not in docs/sitemap.xml, so it is a page nobody is sent to", name)
		}
		if !strings.Contains(page, `<link rel="canonical"`) {
			t.Errorf("%s has no canonical link", name)
		}
		for _, m := range link.FindAllStringSubmatch(page, -1) {
			if !known[m[1]] {
				t.Errorf("%s links to %s, which is not a guide page", name, m[1])
			}
		}
		// Every page has to reach the problem pages, which are the ones people
		// arrive on: a sidebar that lists only the product pages sends a
		// first-time reader back to the search results.
		for _, sibling := range []string{
			"forgetting.html",
			"where-sessions-are-stored.html",
			"after-compaction.html",
			"repeated-mistakes.html",
			"switching-agents.html",
			"export-conversations.html",
			"sync-across-machines.html",
		} {
			if name == sibling {
				continue
			}
			if !strings.Contains(page, `href="`+sibling+`"`) {
				t.Errorf("%s does not link to %s", name, sibling)
			}
		}
	}
}
