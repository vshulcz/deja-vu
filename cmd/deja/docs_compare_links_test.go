package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// compare.html names ten other projects and used to link two of them (#3854).
// Pinning the pair means a column cannot quietly end up pointing at a
// neighbouring project the way CASS did on the day-zero page (#3845) — several of
// these names are taken more than once on GitHub.
func TestCompareLinksEveryProjectItNames(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "compare.html"))
	if err != nil {
		t.Fatal(err)
	}

	// "Agent Session Viewer" is deliberately absent: several Python session
	// viewers match the cell and none of them matches all of it, and a link to
	// the wrong one is the mistake #3845 was.
	unidentified := map[string]bool{"Agent Session Viewer": true}

	want := map[string]string{
		"Mem0":           "mem0ai/mem0",
		"Letta":          "letta-ai/letta",
		"memU":           "NevaMind-AI/memU",
		"Memori":         "MemoriLabs/Memori",
		"claude-mem":     "thedotmack/claude-mem",
		"agentmemory":    "rohitg00/agentmemory",
		"MemPalace":      "MemPalace/mempalace",
		"cognee":         "topoteretes/cognee",
		"Graphiti / Zep": "getzep/graphiti",
		"Hindsight":      "vectorize-io/hindsight",

		"cass":            "Dicklesworthstone/coding_agent_session_search",
		"Agent Sessions":  "jazzyalex/agent-sessions",
		"agent-historian": "adlternative/agent-historian",
		"casr":            "Dicklesworthstone/cross_agent_session_resumer",
	}

	rows := regexp.MustCompile(`(?s)<tr[^>]*><th[^>]*></th><th[^>]*class="us"[^>]*>deja-vu</th>(.*?)</tr>`).FindAllSubmatch(page, -1)
	if len(rows) != 3 {
		t.Fatalf("found %d comparison-table header rows, want 3", len(rows))
	}

	cell := regexp.MustCompile(`(?s)<th[^>]*>(.*?)</th>`)
	href := regexp.MustCompile(`<a href="https://github\.com/([^"]+)"[^>]*>(.*?)</a>`)
	seen := map[string]bool{}
	for _, row := range rows {
		for _, c := range cell.FindAllSubmatch(row[1], -1) {
			got := href.FindSubmatch(c[1])
			if got == nil {
				if unidentified[strings.TrimSpace(string(c[1]))] {
					continue
				}
				t.Errorf("column %q does not link a github repository", strings.TrimSpace(string(c[1])))
				continue
			}
			name := strings.TrimSpace(string(got[2]))
			repo := strings.Trim(string(got[1]), "/")
			seen[name] = true
			if w, ok := want[name]; !ok {
				t.Errorf("column %q is new here; add the repository it belongs to", name)
			} else if repo != w {
				t.Errorf("%s links %s, want %s", name, repo, w)
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("the tables no longer have a %s column", name)
		}
	}
}
