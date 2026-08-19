package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The registry documents where eighteen agents keep their history — the one
// thing about deja people put to a search engine in their own words. Those
// pages existed only as .md, which GitHub Pages serves as text/markdown: no
// title, no description, not in the sitemap. The HTML is generated from the
// markdown, and generated files go stale the moment nobody checks them.
func TestRegistryPagesAreBuiltAndListed(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "registry")
	mds, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(mds) < 18 {
		t.Fatalf("only %d registry pages — the glob is wrong or the pages moved", len(mds))
	}
	sitemap, err := os.ReadFile(filepath.Join("..", "..", "docs", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}

	for _, md := range mds {
		id := strings.TrimSuffix(filepath.Base(md), ".md")
		t.Run(id, func(t *testing.T) {
			htmlPath := filepath.Join(dir, id+".html")
			b, err := os.ReadFile(htmlPath)
			if err != nil {
				t.Fatalf("no page built for %s — run `go run ./scripts/genregistry`: %v", id, err)
			}
			page := string(b)

			src, err := os.ReadFile(md)
			if err != nil {
				t.Fatal(err)
			}
			// Every heading and the verification date have to be in the built
			// page. That is what goes wrong in practice: someone documents a
			// new quirk in the markdown and the page a reader lands on still
			// describes last month's format.
			for _, line := range strings.Split(string(src), "\n") {
				line = strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(line, "## "):
					if h := strings.TrimPrefix(line, "## "); !strings.Contains(page, h) {
						t.Errorf("%s is in the markdown but not in the built page — run `go run ./scripts/genregistry`", h)
					}
				case strings.HasPrefix(line, "**Last verified:**"):
					date := strings.TrimSpace(strings.TrimPrefix(line, "**Last verified:**"))
					if !strings.Contains(page, date) {
						t.Errorf("the page says a different verification date than the markdown (%s)", date)
					}
				}
			}

			// A page with no title and no description is a page a search
			// engine has no reason to show, which is the whole point here.
			if !strings.Contains(page, "<title>") || !strings.Contains(page, `name="description"`) {
				t.Error("the page has no title or no description")
			}
			if !strings.Contains(page, `<link rel="canonical"`) {
				t.Error("the page has no canonical link")
			}
			// Content has to sit inside <article>: .doc is a two-column grid,
			// and children placed directly in it become grid cells that
			// overlap into unreadable text.
			if !strings.Contains(page, "<article>") || !strings.Contains(page, "<aside>") {
				t.Error("the page is not in the site's document layout, so it renders as overlapping columns")
			}
			if !strings.Contains(string(sitemap), "/registry/"+id+".html") {
				t.Errorf("%s.html is not in the sitemap, so nothing will crawl it", id)
			}
		})
	}
}

// The guide links the registry; those links have to reach the built pages
// rather than the raw markdown a browser downloads instead of rendering.
func TestGuideLinksTheBuiltRegistry(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "guide", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "registry/README.md") {
			t.Errorf("%s links the raw markdown, which the browser downloads instead of showing", filepath.Base(p))
		}
	}
}
