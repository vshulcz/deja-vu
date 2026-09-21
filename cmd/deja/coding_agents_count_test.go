package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The other shape the count is written in: "thirty-four coding agents", or
// "thirty-three other coding agents" in a file that ships inside one of them.
// The "named + N more" tests cannot see it, and it is in twelve files —
// including the two plugin READMEs, which had said twenty-five since the
// registry held twenty-five.
func TestEveryCodingAgentCountMatchesTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	phrase := regexp.MustCompile(`(?i)((?:twenty|thirty|forty|fifty)(?:-[a-z]+)?) (other )?coding agents`)

	files := []string{
		"claude-plugin/README.md",
		"codex-plugin/README.md",
		"llms-install.md",
		"docs/ARCHITECTURE.md",
		"docs/guide/harnesses.html",
		"extensions/dsh/README.md",
		"extensions/dsh/package.json",
		"extensions/openclaw/README.md",
		"extensions/opencode/README.md",
		"extensions/opencode/package.json",
		"extensions/pi/README.md",
		// The generator's own doc comment, which said thirty-three while it
		// was rendering thirty-four pages.
		"scripts/genregistry/main.go",
	}
	checked := 0
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		found := phrase.FindAllStringSubmatch(string(b), -1)
		if len(found) == 0 {
			t.Errorf("%s no longer counts coding agents — the phrasing changed and this stopped checking it", f)
			continue
		}
		for _, m := range found {
			// "other" means the file ships inside one of them, so it counts
			// the rest.
			want := countWord(t, n)
			if m[2] != "" {
				want = countWord(t, n-1)
			}
			if got := strings.ToLower(m[1]); got != want {
				t.Errorf("%s says %q%scoding agents; the registry has %d, so it is %q",
					f, got, " "+m[2], n, want)
			}
			checked++
		}
	}
	if checked < len(files) {
		t.Errorf("only %d counts checked across %d files", checked, len(files))
	}
}
