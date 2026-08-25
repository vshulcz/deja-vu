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
