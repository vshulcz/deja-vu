package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The size of a tool answer must be deja's decision, not the caller's. Echoing
// the query whole made a 64 KB query come back as a 64 KB tool result, against
// the ~4 KB the description promises (#1070).
func TestEmptyRecallAnswerDoesNotEchoTheWholeQuery(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{1000, 4000, 16000, 64000} {
		q := strings.Repeat("z", n)
		got := emptyRecallAnswer(dir, q)
		if len(got) > 512 {
			t.Errorf("query of %d bytes produced a %d-byte answer: %.120q…", n, len(got), got)
		}
		if strings.Contains(got, q) {
			t.Errorf("query of %d bytes echoed verbatim", n)
		}
	}
	// Short queries stay whole — the agent needs to see what it asked.
	if got := emptyRecallAnswer(dir, "pgbouncer session mode"); !strings.Contains(got, `"pgbouncer session mode"`) {
		t.Errorf("short query not quoted back: %q", got)
	}
}

// blame's cap was skippable by its own `all` argument: 300 sessions touching
// one path answered 162 KB (#1071).
func TestBlameStaysBoundedWithAll(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 300 {
		id := fmt.Sprintf("b%03d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-mono", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-05-04T10:00:00Z","message":{"role":"user","content":"session ` + id + `: touching internal/webhook/retry.go for the rollout thing"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-05-04T10:01:00Z","message":{"role":"assistant","content":"Edited internal/webhook/retry.go in pass ` + id + `: adjusted the rollout backoff."}}`,
		})
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	text, hits, err := blameTextResult(dir, search.BlameOptions{All: true}, "internal/webhook/retry.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	if hits == 0 {
		t.Fatal("fixture did not produce blame hits")
	}
	if len(text) > blameMCPBudget+512 {
		t.Errorf("blame with all=true answered %d bytes (%d hits); the cap is %d", len(text), hits, blameMCPBudget)
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(text), &arr); err != nil {
		t.Fatalf("blame answer is not a JSON array: %v", err)
	}
	note, _ := arr[len(arr)-1]["note"].(string)
	if !strings.Contains(note, "more sessions touch this path") {
		t.Errorf("truncated blame answer does not say what was left out: last element %v", arr[len(arr)-1])
	}
}
