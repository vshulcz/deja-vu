package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registry.json is checked against the loader list; the human index next to it
// was not checked against anything, and had drifted six harnesses behind — the
// pages for cline, copilot, hermes, kimi, openclaw and roo existed and nothing
// linked to them. A reference nobody can reach is worse than a missing one,
// because it looks complete.
func TestRegistryIndexLinksEveryHarnessAndEveryPage(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "docs", "registry")

	b, err := os.ReadFile(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}

	idx, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	index := string(idx)

	for _, h := range reg.Harnesses {
		if !strings.Contains(index, "["+h.DisplayName+"]") {
			t.Errorf("the registry index has no entry for %s (%s)", h.DisplayName, h.ID)
		}
	}

	pages, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		name := filepath.Base(page)
		if name == "README.md" {
			continue
		}
		if !strings.Contains(index, "("+name+")") {
			t.Errorf("docs/registry/%s exists but nothing links to it", name)
		}
	}
}
