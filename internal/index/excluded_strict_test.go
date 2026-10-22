package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// The agent's own live session holds every word of its question, because the
// question came from it, and recall leaves that session out. With it gone the
// search used to stop at the empty strict tier and answer nothing, though the
// session that settled the question ranked for it (#4766).
func TestExcludingTheOnlyFullMatchFallsThroughToTheRanking(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-pay")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("live", "what jitter constant did we pick for the payments retry backoff")
	write("old", "payments retry backoff: we settled on full jitter with a 30s cap")
	write("readme", "unrelated readme typo fix")
	write("toolchain", "bumped the go toolchain")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	q := "jitter constant payments retry backoff"
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true, ExcludeSessions: map[string]bool{"live": true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) == 0 || r.Sessions[0].ID != "old" {
		ids := make([]string, 0, len(r.Sessions))
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		t.Fatalf("got %v (tier %q), want the session that settled it", ids, r.Tier)
	}
	for _, s := range r.Sessions {
		if s.ID == "live" {
			t.Fatal("the excluded session came back")
		}
	}
	// A regex skips the postings and scans records, which never looked at the
	// exclusion at all.
	rx, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "jitter", Regex: true, All: true, ExcludeSessions: map[string]bool{"live": true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rx.Sessions {
		if s.ID == "live" {
			t.Fatal("a regex search served the excluded session")
		}
	}
	if len(rx.Sessions) == 0 {
		t.Fatal("the regex found nothing, so the exclusion was never exercised")
	}
}
