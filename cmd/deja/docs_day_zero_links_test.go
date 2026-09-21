package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The day-zero table asks a reader to trust six rows of numbers about other
// people's software, so each of those tools names the repository that was
// measured (#3839). Several of the names are taken more than once on GitHub —
// there is more than one "ctx" and more than one "funes" — and the row is only
// checkable if it says which one.
func TestDayZeroLinksEveryToolItCompares(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "guide", "day-zero.html")
	page, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	row := regexp.MustCompile(`(?s)<tr[^>]*><th[^>]*></th><th[^>]*class="us"[^>]*>deja-vu</th>(.*?)</tr>`).FindSubmatch(page)
	if row == nil {
		t.Fatalf("no comparison-table header row in %s", path)
	}
	cells := regexp.MustCompile(`(?s)<th[^>]*>(.*?)</th>`).FindAllSubmatch(row[1], -1)
	if len(cells) < 6 {
		t.Fatalf("header names %d tools besides deja-vu, want at least 6", len(cells))
	}

	// The repository each name belongs to, checked against what the page says
	// about it: CASS was linked to cass_memory_system, another project by the
	// same author, while the version and issue the footnote cites — 0.7.1 and
	// #441 — only exist in coding_agent_session_search.
	want := map[string]string{
		"funes":       "huggingface/funes",
		"ctx":         "ctxrs/ctx",
		"CASS":        "Dicklesworthstone/coding_agent_session_search",
		"agentsview":  "kenn-io/agentsview",
		"agentmemory": "rohitg00/agentmemory",
		"MemPalace":   "MemPalace/mempalace",
		"claude-mem":  "thedotmack/claude-mem",
	}

	href := regexp.MustCompile(`<a href="(https://github\.com/[^"]+)"[^>]*>([^<]+)</a>`)
	seen := map[string]bool{}
	for _, cell := range cells {
		got := href.FindSubmatch(cell[1])
		if got == nil {
			t.Errorf("column %q does not link a github repository", strings.TrimSpace(string(cell[1])))
			continue
		}
		name := strings.TrimSpace(string(got[2]))
		repo := strings.Trim(strings.TrimPrefix(string(got[1]), "https://github.com/"), "/")
		if strings.Count(repo, "/") != 1 {
			t.Errorf("%s is not an owner/repo url", got[1])
			continue
		}
		seen[name] = true
		if w, ok := want[name]; !ok {
			t.Errorf("column %q is new here; add the repository it belongs to", name)
		} else if repo != w {
			t.Errorf("%s links %s, want %s", name, repo, w)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("the table no longer has a %s column", name)
		}
	}
}
