package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A broken JSON-LD block is invisible: the page renders, the browser ignores it,
// and the search engine drops the structured data without telling anyone. One
// page shipped with a quoted command inside a JSON string, which is exactly the
// shape that survives review.
func TestGuidePagesCarryValidStructuredData(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "guide", "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no guide pages found: %v", err)
	}
	block := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	for _, page := range pages {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		found := 0
		for _, m := range block.FindAllStringSubmatch(string(b), -1) {
			found++
			var any any
			if err := json.Unmarshal([]byte(m[1]), &any); err != nil {
				t.Errorf("%s: JSON-LD does not parse — %v", filepath.Base(page), err)
			}
		}
		if found == 0 && !strings.HasSuffix(page, "index.html") {
			t.Errorf("%s: no structured data at all", filepath.Base(page))
		}
	}
}

// Valid JSON is not the same as JSON about this page. These pages are written
// by copying a neighbour, and the two blocks that name the harness are the two
// nobody rereads: four new pages shipped with another harness's breadcrumb and
// its three FAQ answers, which is what Google would have published as the rich
// result for a page about something else.
func TestGuideStructuredDataDescribesItsOwnPage(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "guide", "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no guide pages found: %v", err)
	}
	// Every harness that has a page of its own. A question on one page naming
	// another page's harness is the copy that was never finished — a question
	// naming none of them is just a general one, which is fine.
	var harnesses []string
	for _, page := range pages {
		if rest, ok := strings.CutPrefix(strings.TrimSuffix(filepath.Base(page), ".html"), "memory-for-"); ok {
			first, _, _ := strings.Cut(rest, "-")
			harnesses = append(harnesses, first)
		}
	}

	block := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	for _, page := range pages {
		name := filepath.Base(page)
		harness, ok := strings.CutPrefix(strings.TrimSuffix(name, ".html"), "memory-for-")
		if !ok {
			continue // only the per-harness pages carry a harness name to get wrong
		}
		// claude-code and the like: the first segment is the word both the
		// breadcrumb and the questions use.
		harness, _, _ = strings.Cut(harness, "-")

		foreign := func(text string) string {
			low := strings.ToLower(text)
			if strings.Contains(low, harness) {
				return ""
			}
			for _, other := range harnesses {
				if other != harness && strings.Contains(low, other) {
					return other
				}
			}
			return ""
		}

		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		for _, m := range block.FindAllStringSubmatch(string(b), -1) {
			var doc struct {
				Type            string `json:"@type"`
				ItemListElement []struct {
					Position int    `json:"position"`
					Name     string `json:"name"`
				} `json:"itemListElement"`
				MainEntity []struct {
					Name string `json:"name"`
				} `json:"mainEntity"`
			}
			if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
				continue // the test above already reports a parse failure
			}
			switch doc.Type {
			case "BreadcrumbList":
				for _, item := range doc.ItemListElement {
					if item.Position != 2 {
						continue
					}
					if !strings.Contains(strings.ToLower(item.Name), harness) {
						t.Errorf("%s: breadcrumb reads %q, which does not name %s — the page was copied and this block came with it", name, item.Name, harness)
					}
				}
			case "FAQPage":
				for _, q := range doc.MainEntity {
					if other := foreign(q.Name); other != "" {
						t.Errorf("%s: FAQ asks %q, which is about %s", name, q.Name, other)
					}
				}
			}
		}
	}
}
