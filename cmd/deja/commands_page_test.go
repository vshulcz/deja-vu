package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The CLI reference page is subtitled "every command". It missed five —
// index, mcp, share, handoff and check — and nothing noticed, because the page
// is only checked for its title and its counts (#3356).
//
// Two commands are named differently on purpose: the page describes the bare
// `deja` screen rather than `deja brief`, and `deja "query"` rather than the
// `deja search` alias that exists so a query may start with a dash. Hidden
// hooks are not commands a reader types.
func TestTheCommandsPageNamesEveryCommand(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "commands.html"))
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, m := range regexp.MustCompile(`<code>deja ([a-z-]+)`).FindAllStringSubmatch(string(page), -1) {
		named[m[1]] = true
	}
	if len(named) < 20 {
		t.Fatalf("the page names %d commands, so this checks nothing", len(named))
	}

	spelledOtherwise := map[string]bool{"brief": true, "search": true}
	var missing []string
	for _, line := range strings.Split(usageText(), "\n") {
		// A name, not a flag: the usage lists `deja --harness name` under the
		// bare query as well.
		m := regexp.MustCompile(`^\s+deja ([a-z][a-z-]*)`).FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if strings.HasPrefix(name, "hook-") || spelledOtherwise[name] || named[name] {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		t.Errorf("docs/guide/commands.html says \"every command\" and has no row for %v", missing)
	}
}
