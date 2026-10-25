package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The grid under "Works with 25" listed twenty names: Amp, Continue, Copilot
// Chat, prime-agent and Crush landed and never reached it, so the heading
// counted five agents the grid did not show. It is the list a reader scans for
// their own agent before installing anything.
func TestTheHomeGridShowsEveryHarnessItCounts(t *testing.T) {
	root := filepath.Join("..", "..")
	page, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	grid := regexp.MustCompile(`(?s)<div class="agents".*?</div>`).Find(page)
	if grid == nil {
		t.Fatal("the home page has no agent grid to check")
	}
	shown := 0
	linked := map[string]bool{}
	for _, m := range regexp.MustCompile(`<span>(?:<a href="registry/([^"]+)">)?([^<]+)(?:</a>)?</span>`).FindAllSubmatch(grid, -1) {
		if strings.TrimSpace(string(m[2])) != "" {
			shown++
		}
		linked[string(m[1])] = true
	}
	if n := registryHarnessCount(t, root); shown != n {
		t.Errorf("the grid shows %d agents under a heading that counts %d", shown, n)
	}

	// The registry pages had no link from the home page, and none of them was
	// in Google's index. Each name in the grid links its registry page.
	pages, err := filepath.Glob(filepath.Join(root, "docs", "registry", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		if name := filepath.Base(p); name != "README.html" && !linked[name] {
			t.Errorf("the home grid does not link registry/%s", name)
		}
	}
	if !strings.Contains(string(grid), `href="registry/README.html"`) {
		t.Error("the home grid does not link the registry index")
	}
}
