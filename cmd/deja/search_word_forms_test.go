package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Asking with the singular over transcripts that use the -ies plural returned
// "no matches in N indexed sessions" — advice about wording, for a word the
// store holds in another form (#1079).
func TestSearchFindsTheIesPluralFromTheSingular(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One topic per written form, so a hit names which session answered.
	for _, tc := range []struct{ id, text string }{
		{"s-winch", "the winch subsystem: retries handled during service"},
		{"s-macerator", "the macerator subsystem: queries handled during service"},
		{"s-alternator", "the alternator subsystem: proxies handled during service"},
	} {
		var lines []string
		for i := 0; i < 3; i++ {
			lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/tmp/stem","timestamp":"2026-06-0%dT10:00:00Z","message":{"role":"user","content":%q}}`, tc.id, i+1, tc.text+fmt.Sprintf(" line %d", i)))
		}
		writeClaudeFixture(t, filepath.Join(root, "projects", "-tmp-stem", tc.id+".jsonl"), tc.id, lines)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for q, want := range map[string]string{
		"query": "s-macerator",
		"proxy": "s-alternator",
	} {
		out, err := captureRun(t, "search", "--json", q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		var got struct {
			Tier string `json:"tier"`
			Hits []struct {
				Session struct {
					ID string `json:"id"`
				} `json:"session"`
			} `json:"hits"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%q: json: %v\n%s", q, err, out)
		}
		if len(got.Hits) == 0 {
			t.Errorf("%q answered nothing over a store that holds it as a plural", q)
			continue
		}
		var ids []string
		for _, h := range got.Hits {
			ids = append(ids, h.Session.ID)
		}
		if ids[0] != want {
			t.Errorf("%q returned %v, want %s first", q, ids, want)
		}
	}

	// And the plural still finds the singular: the rule works both ways.
	out, err := captureRun(t, "search", "retries")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "s-winch") {
		t.Errorf("the control query lost its own session:\n%s", out)
	}
}
