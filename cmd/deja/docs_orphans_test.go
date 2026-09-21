package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Two pages sat in the sitemap with nothing on the site linking to them (#3840,
// #3841): one was left out of a sidebar copied onto 58 pages, the other was a
// utility the pages documenting its flag never mentioned. A sitemap entry that
// no page links is the weakest thing a sitemap can hold, and nothing noticed
// either for weeks, so the crawl is a test.
func TestEverySitemapPageIsReachableFromTheIndex(t *testing.T) {
	docs := filepath.Join("..", "..", "docs")
	const site = "https://vshulcz.github.io/deja-vu/"

	sitemap, err := os.ReadFile(filepath.Join(docs, "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	rel := func(u string) string {
		p := strings.TrimPrefix(u, site)
		if p == "" || strings.HasSuffix(p, "/") {
			p += "index.html"
		}
		return p
	}
	var listed []string
	for _, m := range regexp.MustCompile(`<loc>([^<]+)</loc>`).FindAllStringSubmatch(string(sitemap), -1) {
		if strings.HasPrefix(m[1], site) {
			listed = append(listed, rel(m[1]))
		}
	}
	if len(listed) < 100 {
		t.Fatalf("sitemap lists %d pages under %s, want at least 100", len(listed), site)
	}

	href := regexp.MustCompile(`href="([^"]+)"`)
	links := func(page string) []string {
		body, err := os.ReadFile(filepath.Join(docs, page))
		if err != nil {
			return nil
		}
		var out []string
		for _, m := range href.FindAllStringSubmatch(string(body), -1) {
			h := m[1]
			if strings.HasPrefix(h, site) {
				out = append(out, rel(h))
				continue
			}
			if strings.Contains(h, "://") || strings.HasPrefix(h, "#") || strings.HasPrefix(h, "//") ||
				strings.HasPrefix(h, "mailto:") || strings.HasPrefix(h, "data:") {
				continue
			}
			h = strings.SplitN(strings.SplitN(h, "#", 2)[0], "?", 2)[0]
			if h == "" {
				continue
			}
			if strings.HasSuffix(h, "/") {
				h += "index.html"
			}
			out = append(out, filepath.ToSlash(filepath.Join(filepath.Dir(page), h)))
		}
		return out
	}

	seen := map[string]bool{"index.html": true}
	for queue := []string{"index.html"}; len(queue) > 0; {
		page := queue[0]
		queue = queue[1:]
		for _, next := range links(page) {
			if seen[next] || filepath.Ext(next) != ".html" {
				continue
			}
			if _, err := os.Stat(filepath.Join(docs, next)); err != nil {
				continue
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}

	var orphans []string
	for _, page := range listed {
		if !seen[page] {
			orphans = append(orphans, page)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("%d of the %d sitemap pages are linked from nowhere: %s",
			len(orphans), len(listed), strings.Join(orphans, ", "))
	}
}
