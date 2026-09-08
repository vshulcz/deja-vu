package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The home page's `sources` command prints the harness names one by one and
// then a count. Crush landed as the 25th, every count on the site moved with
// it, and this list did not: it said 25 while naming 24. A list of names goes
// stale the same way a number does, and it is the one place a reader checks
// for their own agent.
func TestTheHomePageNamesEveryHarnessItCounts(t *testing.T) {
	root := filepath.Join("..", "..")
	page, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`'sources':'<p class="none">(.*?)\\n(\d+) harnesses`).FindSubmatch(page)
	if line == nil {
		t.Fatal("the home page has no `sources` line to check")
	}
	named := map[string]bool{}
	for _, n := range strings.Fields(strings.ReplaceAll(string(line[1]), "&nbsp;", " ")) {
		named[n] = true
	}

	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID string `json:"id"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	// The registry ids are the doc page names; the line prints what `deja
	// sources` calls each harness, and the two differ for a handful.
	alias := map[string]string{
		"claude-code": "claude",
		"continue":    "continue",
		"copilot-cli": "copilot",
	}
	missing := []string{}
	count := 0
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		count++
		name := h.ID
		if a, ok := alias[h.ID]; ok {
			name = a
		}
		if !named[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the home page counts %d harnesses and names neither of %v", count, missing)
	}
	if len(named) != count {
		t.Errorf("the home page names %d harnesses and says %d", len(named), count)
	}
}
