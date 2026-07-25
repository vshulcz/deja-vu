package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// The relevance tier took the global top 50 and only then applied the search
// scope, so a filtered search returned nothing whenever the unfiltered head
// was full of sessions from another harness or project — the deeper matches
// the filter was asking for had already been cut.
func TestRelevanceTierAppliesScopeBeforeTruncation(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	codexRoot := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// 60 Claude sessions matching three of the four query terms, so they all
	// outrank the Codex one and fill the top 50 on their own.
	for i := 0; i < 60; i++ {
		line := fmt.Sprintf(`{"type":"user","sessionId":"c%d","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"kubernetes autoscaler telemetry notes %d"}}`, i, i) + "\n"
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("c%d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A session holding the fourth term and nothing else, so every query term
	// resolves (the close tier drops terms it cannot resolve and ANDs the
	// rest) while no session holds all four — which is what drops the ladder
	// down to the relevance tier.
	if err := os.WriteFile(filepath.Join(proj, "d1.jsonl"),
		[]byte(`{"type":"user","sessionId":"d1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"dashboards"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// One Codex session matching two of the terms — a genuine answer for a
	// harness-scoped search, and ranked below every Claude session.
	sess := filepath.Join(codexRoot, "sessions", "2026", "01", "02")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	roll := `{"type":"session_meta","payload":{"id":"cx1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/app"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"user_message","message":"kubernetes autoscaler runbook"},"timestamp":"2026-01-02T03:04:06Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(sess, "rollout-2026-01-02T03-04-05-cx1.jsonl"), []byte(roll), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// No session holds all four terms, so the ladder falls to the relevance
	// tier — which is where the truncation happened.
	const q = "kubernetes autoscaler telemetry dashboards"
	all, err := SearchWithRecoveryDetailed(dir, search.Options{Query: q, All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Tier != search.TierRelevance {
		t.Fatalf("fixture resolved through tier %q, want the relevance tier", all.Tier)
	}
	got, err := SearchWithRecoveryDetailed(dir, search.Options{Query: q, Harness: "codex", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) == 0 {
		t.Fatal("harness-scoped relevance search returned nothing; the match was cut by truncation before the scope filter ran")
	}
	for _, s := range got.Sessions {
		if s.Harness != "codex" {
			t.Fatalf("scope leaked: got a %s session", s.Harness)
		}
	}
}
