package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

func TestLadderFallsBackToRelevance(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The relevance tier exists for queries whose indexed words are spread
	// across sessions: no single session holds even a dropped-down AND.
	write("a.jsonl", "graduate", "quetzal figment bartleby snorkel discussed at length")
	write("b.jsonl", "other", "marzipan wombat cufflink review notes")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A natural question: no session contains every word, so exact/stem/fuzzy
	// all miss — the relevance tier must surface the graduation session.
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "quetzal figment bartleby marzipan wombat cufflink", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want relevance (sessions=%d)", r.Tier, len(r.Sessions))
	}
	if len(r.Sessions) != 2 {
		t.Fatalf("both partially-matching sessions must surface, got %d", len(r.Sessions))
	}
	if r.Sessions[0].ID != "graduate" { // 4 informative hits outrank 3
		t.Fatalf("relevance order wrong: %s first", r.Sessions[0].ID)
	}
	// Single informative word still prefers silence over noise.
	r2, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "zzznothing", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Sessions) != 0 {
		t.Fatalf("nonsense query must stay empty, got %d sessions", len(r2.Sessions))
	}
}
