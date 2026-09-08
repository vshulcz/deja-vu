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
	for _, m := range regexp.MustCompile(`<span>([^<]+)</span>`).FindAllSubmatch(grid, -1) {
		if strings.TrimSpace(string(m[1])) != "" {
			shown++
		}
	}
	if n := registryHarnessCount(t, root); shown != n {
		t.Errorf("the grid shows %d agents under a heading that counts %d", shown, n)
	}
}
