package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// The relevance tier used to load every record of every candidate session and
// fold all of it to find the few messages that matched: on a real store one
// query folded 120.7 MB across 140,355 messages, 10.3% of which held any query
// term. It reads the records the ranking already identified now, so a hit
// carries the messages that matched — and the pool, the order and the totals
// are what they were (#3491).
func TestTheRelevanceTierReadsTheRecordsThatMatched(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// One session with the answer buried in the middle of a long transcript,
	// and a pile of others so the query has a corpus to be ranked against.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		text := fmt.Sprintf("ordinary line %d about the sidebar layout", i)
		switch i {
		case 150:
			text = "the billing exporter drops every third retry"
		case 151:
			text = "the backoff counted from zero in the scheduler"
		}
		fmt.Fprintf(&b, `{"type":"user","sessionId":"long","cwd":"/work/app","timestamp":"2026-09-0%dT10:%02d:00Z","message":{"role":"user","content":%q}}`+"\n",
			1+i%9, i%60, text)
	}
	if err := os.WriteFile(filepath.Join(proj, "long.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 12; k++ {
		id := fmt.Sprintf("other%d", k)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/app","timestamp":"2026-09-02T11:%02d:00Z","message":{"role":"user","content":"work on the invoice renderer and the webpack config"}}`+"\n", id, k)
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Relevance alone: one token the store has never seen empties the
	// substring intersection the close tier would otherwise answer with, and
	// the rest are informative enough to rank.
	res, err := SearchWithRecoveryDetailed(dir, query.Options{
		Query: "billing exporter scheduler quibnotpresentword", Limit: 10,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want relevance", res.Tier)
	}
	var found bool
	for _, s := range res.Sessions {
		if s.ID != "long" {
			continue
		}
		found = true
		if len(s.Messages) == 0 {
			t.Fatal("the hit carries no messages")
		}
		// The matched message is there, and the 299 that matched nothing are
		// not: that is the whole of the change.
		joined := ""
		for _, m := range s.Messages {
			joined += m.Text + "\n"
		}
		for _, want := range []string{"billing exporter drops every third retry", "backoff counted from zero in the scheduler"} {
			if !strings.Contains(joined, want) {
				t.Errorf("the matched message %q is missing:\n%s", want, joined)
			}
		}
		if len(s.Messages) > 20 {
			t.Errorf("the hit carries %d messages of a 300-message session", len(s.Messages))
		}
		if strings.Contains(joined, "ordinary line 10 ") {
			t.Errorf("a message that matched nothing was read:\n%s", joined)
		}
	}
	if !found {
		t.Fatalf("the session that answers the question is not in the answer (%d hits)", len(res.Sessions))
	}
}
