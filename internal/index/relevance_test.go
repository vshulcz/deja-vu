package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

func TestLadderFallsBackToRelevance(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
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
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "quetzal figment bartleby snorkel marzipan wombat cufflink", All: true}, nil)
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

// The relevance tier ranks the whole candidate pool and serves the top
// relevanceWindow of it, so the sessions it returns are the window rather than
// the match. Counting them and calling that the total reported the window's own
// size on every query deeper than it, with capped: false alongside — a missing
// signal sends a consumer to check, a wrong one tells it to stop.
func TestRelevanceReportsThePoolItsWindowTrimmed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates int
		wantTotal  int
		wantServed int
		wantCapped bool
	}{
		// One session per candidate plus the one holding the fourth term,
		// which the ranking scores too: it is the pool, not the answer set.
		{"pool deeper than the window", 60, 61, relevanceWindow, true},
		{"pool inside the window", 10, 11, 11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := relevancePoolFixture(t, tc.candidates)
			const q = "kubernetes autoscaler telemetry dashboards"
			r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Guard the fixture, not the fix: if the ladder stops falling
			// through to relevance the numbers below mean nothing.
			if r.Tier != query.TierRelevance {
				t.Fatalf("fixture resolved through tier %q, want the relevance tier", r.Tier)
			}
			if len(r.Sessions) != tc.wantServed {
				t.Fatalf("served %d sessions, want %d", len(r.Sessions), tc.wantServed)
			}
			if r.Total != tc.wantTotal {
				t.Fatalf("total = %d, want %d — every session the ranking scored, before the window", r.Total, tc.wantTotal)
			}
			if r.Capped != tc.wantCapped {
				t.Fatalf("capped = %v with %d of %d sessions served", r.Capped, len(r.Sessions), r.Total)
			}
			// The invariant the reporting exists for: capped false has to mean
			// the caller is holding everything that matched.
			if !r.Capped && r.Total != len(r.Sessions) {
				t.Fatalf("capped is false while %d of %d matches were withheld", r.Total-len(r.Sessions), r.Total)
			}
		})
	}
}

// relevancePoolFixture indexes n sessions carrying three of the four query
// terms and one carrying only the fourth, so every term resolves in the corpus
// while no session holds them all — which is what drops the ladder to the
// relevance tier — and the scored pool is n+1.
func relevancePoolFixture(t *testing.T, n int) string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%d", i)
		write(id+".jsonl", id, fmt.Sprintf("kubernetes autoscaler telemetry notes %d", i))
	}
	write("d1.jsonl", "d1", "dashboards")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
