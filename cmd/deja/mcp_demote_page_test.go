package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Demotion used to run on the page rather than on the result set. With more
// matches than the limit, every hit on the page was rejected, so moving them
// down was a no-op — and the attempts the reader had not rejected sat below the
// cut and never reached the agent. Measured: 12 matches, the 9 newest rejected,
// limit 6 served 6 rejected attempts and none of the 3 clean ones.
func TestMCPRecallDemotesBeforePaging(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	for n := range 12 {
		id := fmt.Sprintf("gx%02d", n)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-05-01T10:%02d:00Z","message":{"role":"user","content":"gribbleflux attempt %d"}}`, id, n, n),
		})
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The nine newest are the rejected ones, so recency alone puts them on top.
	for n := 3; n < 12; n++ {
		id := fmt.Sprintf("gx%02d", n)
		if err := runPromote(dir, []string{id, "--state", "rejected", "--note", "strategy " + id + " did not hold"}, io.Discard); err != nil {
			t.Fatalf("promote %s: %v", id, err)
		}
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	text, err := recallText(dir, "gribbleflux", "", 6, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var clean int
	for n := range 3 {
		if strings.Contains(text, fmt.Sprintf("gribbleflux attempt %d", n)) {
			clean++
		}
	}
	if clean != 3 {
		t.Errorf("page of 6 carried %d of the 3 sessions the reader did not reject:\n%s", clean, text)
	}
	if !strings.Contains(text, "marked rejected") {
		t.Errorf("order changed and the answer did not say so:\n%s", text)
	}

	// A page with no rejected hit on it must not spend a line saying "0".
	first, err := recallText(dir, "gribbleflux", "", 3, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, "0 session") {
		t.Errorf("counted zero rejected sessions out loud:\n%s", first)
	}
}
