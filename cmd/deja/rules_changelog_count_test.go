package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The unreleased changelog says how many agents `deja rules sync` writes to.
// Adding one to rulesHarnesses without the sentence left Junie out of the
// count, so the number is pinned to the list until the release ships.
func TestChangelogRulesSyncCountIsTheList(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	start := strings.Index(text, "## [Unreleased]")
	if start < 0 {
		t.Skip("no unreleased section")
	}
	unreleased := text[start+len("## [Unreleased]"):]
	if end := strings.Index(unreleased, "\n## "); end >= 0 {
		unreleased = unreleased[:end]
	}
	m := regexp.MustCompile("`deja rules sync` writes to (\\d+) agents").FindStringSubmatch(unreleased)
	if m == nil {
		t.Skip("unreleased section does not count rules sync agents")
	}
	if n, _ := strconv.Atoi(m[1]); n != len(rulesHarnesses) {
		t.Errorf("CHANGELOG says rules sync writes to %d agents; rulesHarnesses has %d", n, len(rulesHarnesses))
	}
}
