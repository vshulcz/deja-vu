package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// seedFiller writes n sessions of unrelated chatter, so the store is bigger
// than the window the relevance tail is drawn from. Below that size the tail
// stays off by design and none of this applies.
func seedFiller(t *testing.T, proj string, n int) {
	t.Helper()
	for i := range n {
		sid := fmt.Sprintf("filler-%d", i)
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"routine standup chatter number ` + fmt.Sprint(i) + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeSession(t *testing.T, proj, sid, text string) {
	t.Helper()
	line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedStore(t *testing.T, filler int) string {
	t.Helper()
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
	// The session that answers the question, worded the way people actually
	// write: it never says "own", so an AND over the question's words cannot
	// reach it. It does carry two of them, which is the bar relevance sets —
	// one lucky word is noise.
	writeSession(t, proj, "answer", "not that many bikes really, both of mine live in the hallway")
	// An incidental session that does satisfy the AND, on a completely
	// different subject. This is what a strict match lands on.
	writeSession(t, proj, "incidental", "many courier bikes own their routes downtown")
	seedFiller(t, proj, filler)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestThinANDGetsRelevanceTailBeneathIt(t *testing.T) {
	dir := seedStore(t, relevanceWindow+10)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) < 2 {
		t.Fatalf("tail did not run: %d session(s), tier %q", len(r.Sessions), r.Tier)
	}
	// The strict match keeps the first position. Relevance ranked alone scores
	// worse there, so the tail must never push it down.
	if r.Sessions[0].ID != "incidental" {
		t.Fatalf("strict hit lost the top spot to %q", r.Sessions[0].ID)
	}
	found := false
	for _, s := range r.Sessions {
		if s.ID == "answer" {
			found = true
		}
	}
	if !found {
		ids := make([]string, 0, len(r.Sessions))
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		t.Fatalf("the answer the AND excluded is still unreachable: %v", ids)
	}
	// Order is the contract: RelevanceHits scores by arrival, so the merged
	// list has to be labelled as the tier that preserves it.
	if r.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want relevance so the merged order survives", r.Tier)
	}
}

func TestSmallStoreKeepsItsStrictAnswerAlone(t *testing.T) {
	dir := seedStore(t, 3)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Few results out of few sessions is an answer, not a symptom. Ranking a
	// store smaller than the window returns most of it, which would attach the
	// rest of the history to a precise query.
	for _, s := range r.Sessions {
		if s.ID != "incidental" {
			t.Fatalf("small store got a tail: %q came back too", s.ID)
		}
	}
}

func TestWideANDIsLeftAlone(t *testing.T) {
	dir := seedStore(t, relevanceWindow+10)

	// A query the filler sessions all satisfy: the intersection is describing
	// a real cluster, so nothing should be appended to it.
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "routine standup chatter", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) < thinAND {
		t.Fatalf("expected a wide AND, got %d sessions", len(r.Sessions))
	}
	for _, s := range r.Sessions {
		if s.ID == "answer" {
			t.Fatal("a wide AND was widened further by the tail")
		}
	}
}
