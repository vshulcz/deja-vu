package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// writeMessages puts one session on disk carrying a message per line, for the
// tests that need many postings rather than many sessions.
func writeMessages(t *testing.T, proj, sid, body string) {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		b.WriteString(`{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + line + `"}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedStore(t *testing.T, filler int) string {
	t.Helper()
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
	// The strict hit is inside the pool the ranking scored, so counting it
	// again would tell the caller there is more behind the window than there
	// is. The total may never be smaller than what was handed back either.
	if r.Total < len(r.Sessions) {
		t.Fatalf("total %d is under the %d sessions returned", r.Total, len(r.Sessions))
	}
	if r.Total > len(r.Sessions) != r.Capped {
		t.Fatalf("capped=%v disagrees with total %d over %d sessions", r.Capped, r.Total, len(r.Sessions))
	}
}

// The label says nothing matched; the answer holds a session that matched
// every word. Which one, and how many, has to survive to the caller or every
// surface above repeats the label's claim (#3815).
func TestTheStrictHeadIsCountedAndNamedUnderTheRelevanceLabel(t *testing.T) {
	dir := seedStore(t, relevanceWindow+10)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want the relabelled answer this is about", r.Tier)
	}
	if r.Strict != 1 {
		t.Fatalf("strict = %d, want the one session that satisfied the AND", r.Strict)
	}
	var strict, ranked int
	for _, s := range r.Sessions {
		switch {
		case s.ID == "incidental" && r.IsStrict(s):
			strict++
		case s.ID == "answer" && !r.IsStrict(s):
			ranked++
		case r.IsStrict(s):
			t.Fatalf("%q was ranked into the answer but is named as a match", s.ID)
		}
	}
	if strict != 1 {
		t.Fatal("the session that holds every query word is not named as one")
	}
	if ranked != 1 {
		t.Fatal("the session the tail brought in is missing, or is named a match")
	}
}

// A quoted phrase that matches nothing is retried without its quotes, and the
// retry is published under the relevance label so the loosening is visible.
// The words can all be there — only the phrase was not — and then the label
// says nothing matched about an answer that did (#3815).
func TestAQuotedQueryRetriedWithoutItsQuotesSaysWhatMatched(t *testing.T) {
	dir := seedStore(t, 3)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: `courier bikes "downtown routes"`, All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want the relabelled retry this is about", r.Tier)
	}
	if len(r.Sessions) == 0 {
		t.Fatal("the retry found nothing, so there is nothing to label")
	}
	if r.Strict != len(r.Sessions) {
		t.Fatalf("strict = %d over %d sessions, want every one of them", r.Strict, len(r.Sessions))
	}
	for _, s := range r.Sessions {
		if !r.IsStrict(s) {
			t.Fatalf("%q matched the loosened query but is not named as a match", s.ID)
		}
	}
}

func TestWordFormFallbackKeepsItsAnnotationUnderTheTail(t *testing.T) {
	dir := seedStore(t, relevanceWindow+10)

	// "biked" has no postings of its own; the ladder reaches the corpus
	// through the stem fold, and that fallback is worth telling the user
	// about even once relevance owns the order.
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many biked hallway", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) == 0 {
		t.Skip("no word-form fallback for this corpus")
	}
	if r.Stemmed && len(r.Variants) == 0 {
		t.Fatal("the stemmed notice survived but the word forms behind it did not")
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
	// Strict counts the head of a relabelled answer. This answer kept its own
	// tier, so there is nothing for it to disambiguate and it stays zero.
	if r.Strict != 0 || r.StrictIDs != nil {
		t.Fatalf("an answer on tier %q reported %d strict sessions", r.Tier, r.Strict)
	}
	for _, s := range r.Sessions {
		if s.ID == "answer" {
			t.Fatal("a wide AND was widened further by the tail")
		}
	}
}

// A store of fifty sessions is the size a LongMemEval haystack has, and the
// size a real store has for the first weeks after an install. It used to get no
// tail at all: the strict answer came back alone, and the session that actually
// answered the question was never returned. That cost 1.8pp of hit@5 on
// LongMemEval-S and 5.2pp on LoCoMo.
func TestAStoreTheSizeOfAHaystackGetsATail(t *testing.T) {
	dir := seedStore(t, relevanceWindow-5)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("the session that answers the question never came back: %v", sessionIDs(r.Sessions))
	}
}

// The tail is a handful of places, not the store: a fifth of it, capped. Without
// a cap a precise query on a small store comes back with most of the history
// attached, which is what the old all-or-nothing guard was protecting against.
func TestTheTailOnASmallStoreIsAFifthOfIt(t *testing.T) {
	const (
		unrelated = 35
		rivals    = 12
	)
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
	writeSession(t, proj, "incidental", "many courier bikes own their routes downtown")
	// Sessions the ranking will score: each carries two of the question's
	// words, and there are more of them than the cap allows, so the cap is the
	// only thing deciding how much of the pool is served.
	for i := range rivals {
		writeSession(t, proj, fmt.Sprintf("rival-%d", i),
			fmt.Sprintf("the bikes i own live in hallway number %d", i))
	}
	seedFiller(t, proj, unrelated)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := unrelated + rivals + 1
	want := 1 + min(thinAND, store/5) // the strict head plus its tail
	if len(r.Sessions) > want {
		t.Errorf("tail of %d sessions over a store of %d, want at most %d",
			len(r.Sessions)-1, store, want-1)
	}
	if len(r.Sessions) < 2 {
		t.Errorf("no tail at all over a store of %d", store)
	}
}

// Below the floor the tail stands down: on a handful of sessions the one it
// would add arrives on filler words, not on the subject.
func TestBelowTheFloorThereIsNoTail(t *testing.T) {
	dir := seedStore(t, tailFloor-4) // plus the two named sessions: under the floor

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Sessions {
		if s.ID != "incidental" {
			t.Fatalf("a store under the floor got a tail: %q came back", s.ID)
		}
	}
}
