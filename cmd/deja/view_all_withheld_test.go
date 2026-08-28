package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A page a rule emptied read as a machine with no history: no sessions, a bare
// arrow for the date range, and a note counting zero "most recent sessions".
// The reason was on stderr, and the file is the thing people look at (#2321).
func TestViewSaysWhenARuleEmptiedThePage(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own connection pool question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":false,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	out := filepath.Join(t.TempDir(), "view.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	if strings.Contains(page, "connection pool question") {
		t.Fatalf("the withheld session is on the page, so this measures nothing")
	}
	if !strings.Contains(page, "trust policy") {
		t.Errorf("the page does not say a rule emptied it:\n%s", pageStatsBlock(page))
	}
	if strings.Contains(page, "the 0 most recent sessions") {
		t.Errorf("the page counts zero most-recent sessions:\n%s", pageStatsBlock(page))
	}
	if strings.Contains(page, "<b> → </b>") {
		t.Errorf("the page renders an empty date range:\n%s", pageStatsBlock(page))
	}
}

// pageStatsBlock is the head of the page body, for a readable failure.
func pageStatsBlock(page string) string {
	i := strings.Index(page, `class="stats"`)
	if i < 0 {
		return "(no stats block)"
	}
	end := i + 600
	if end > len(page) {
		end = len(page)
	}
	return page[i:end]
}
