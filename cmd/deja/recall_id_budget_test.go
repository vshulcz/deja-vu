package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// recall handed a session id returned the session's digest before any of the
// budget logic ran, so the cap it was called with did not apply: six of the
// twenty largest sessions on a real store came back over it, by up to 3653
// bytes (#4023). The id door is a deep read, so it gets recall_context's room
// and cut marker rather than recall's own page size.
func TestRecallByIDKeepsToABudget(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "jjjj0004-4444-4000-8000-c3d4e5f6a7b8"
	words := []string{"vacuum", "reindex", "partition", "backfill", "rollover", "compaction", "failover"}
	var b strings.Builder
	for i := 0; i < 80; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		text := fmt.Sprintf("step %d: replica lag on shard %d reached %d seconds after the %s migration, so the %s check was rerun against host-%d with a %d ms window", i, i%13, 40+i, words[i%len(words)], words[(i+3)%len(words)], i*7, 250+i)
		b.WriteString(`{"type":"` + role + `","message":{"role":"` + role + `","content":` + quoteJSON(text) +
			`},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	budget := recallMCPBudget - recallFrameOverhead
	text, n, _, ids, _, err := recallTextResultFrom(dir, "jjjj0004", "", 5, 0, budget)
	if err != nil || n != 1 || len(ids) != 1 || ids[0] != id {
		t.Fatalf("the id did not resolve: n=%d ids=%v err=%v", n, ids, err)
	}
	if limit := budget + contextMCPBudget - recallMCPBudget; len(text) > limit {
		t.Errorf("recall by id came back at %d bytes, over the %d it has", len(text), limit)
	}
	if !strings.HasSuffix(text, contextDigestCut) {
		t.Errorf("a cut digest has to say it was cut:\n%s", text[len(text)-200:])
	}
	// Deep, not a page: the id door keeps more than recall's own 4096.
	if len(text) <= recallMCPBudget {
		t.Errorf("recall by id cut to %d bytes, the page size, not the deep-read size", len(text))
	}
}
