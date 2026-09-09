package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The agents page is where a reader learns what the MCP tool can do: it names
// each mode in a table and counts them in the sentence above it. Both are
// hand-written beside an enum that has changed twice — `recall_context` became
// `context`, and the six became one tool with a mode — so the page is checked
// against the enum rather than trusted.
func TestTheAgentsPageNamesEveryMCPMode(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "agents.html"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CTX_MCP", "")
	defaultModes := mcpToolModes(t)
	t.Setenv("DEJA_CTX_MCP", "1")
	modes := mcpToolModes(t)
	if len(modes) == 0 {
		t.Fatal("the tool schema declares no modes, so this checks nothing")
	}
	for _, m := range modes {
		if !strings.Contains(string(page), "<code>"+m+"</code>") {
			t.Errorf("docs/guide/agents.html never names the %q mode", m)
		}
	}
	// And the count in the sentence above the table.
	said := regexp.MustCompile(`([a-z]+) capabilities behind one entry`).FindSubmatch(page)
	if said == nil {
		t.Fatal("the page no longer says how many capabilities there are")
	}
	spelled := map[string]int{"four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "fourteen": 14, "sixteen": 16}
	got, ok := spelled[string(said[1])]
	if !ok {
		got, _ = strconv.Atoi(string(said[1]))
	}
	if got != len(defaultModes) {
		t.Errorf("the page says %q capabilities; the tool declares %d", said[1], len(defaultModes))
	}
}

// mcpToolModes reads the runtime schema, including the opt-in extension, so
// documentation is checked against what tools/list actually advertises.
func mcpToolModes(t *testing.T) []string {
	t.Helper()
	tool := ctxListedTool(t)
	props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
	return props["mode"].(map[string]any)["enum"].([]string)
}
