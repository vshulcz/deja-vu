package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A store that hands the whole session over on every pass — goose does, because
// its query filters on the session's updated_at as well as the message's — used
// to add the same messages again each time, so the most active session in the
// store silently deranked itself (#1304, defect 2).
func TestARedeliveredSessionIsFoldedOnce(t *testing.T) {
	ms := []model.Message{
		{Role: "user", Text: "why does the retry queue stall"},
		{Role: "assistant", Text: "the backoff is capped at one second"},
	}
	var meta SessionMeta
	extendDerived(&meta, ms)
	first := meta.Words
	if first == 0 {
		t.Fatal("nothing was counted")
	}
	for i := 0; i < 3; i++ {
		extendDerived(&meta, ms)
	}
	if meta.Words != first {
		t.Errorf("the same delivery counted again: %d words after one pass, %d after four", first, meta.Words)
	}
	grown := append(append([]model.Message(nil), ms...), model.Message{Role: "user", Text: "it stalls again on staging"})
	extendDerived(&meta, grown)
	if meta.Words <= first {
		t.Errorf("a message appended to a re-delivered session was not counted: %d words", meta.Words)
	}
	if meta.Counted != 3 {
		t.Errorf("counted %d messages, session holds 3", meta.Counted)
	}
}

// A file yields only the region appended since last time, so the tail arrives
// with no overlap at all — the opposite delivery from the one above, and the
// reason a plain count could not tell them apart.
func TestATailWithNoOverlapIsFoldedWhole(t *testing.T) {
	var meta SessionMeta
	extendDerived(&meta, []model.Message{{Role: "user", Text: "why does the retry queue stall"}})
	before := meta.Words
	extendDerived(&meta, []model.Message{
		{Role: "assistant", Text: "the backoff is capped at one second"},
		{Role: "user", Text: "raise it and redeploy"},
	})
	if meta.Counted != 3 {
		t.Errorf("counted %d, three messages have arrived", meta.Counted)
	}
	if meta.Words <= before {
		t.Errorf("the appended tail was not counted: %d words", meta.Words)
	}
}

// The caps are the reason the manifest stays small, and it is read on every
// search. A plain union grew on every append (#1304, defect 4).
func TestDerivedFieldsStayCapped(t *testing.T) {
	var meta SessionMeta
	for round := 0; round < 6; round++ {
		var ms []model.Message
		for i := 0; i < 6; i++ {
			ms = append(ms, model.Message{
				Role: "user",
				Text: "how do I wire the " + string(rune('a'+round)) + string(rune('a'+i)) + " adapter into the pipeline, and what should it return?",
			})
		}
		extendDerived(&meta, ms)
	}
	if len(meta.Asked) > askedQuestionCap {
		t.Errorf("Asked grew past its cap: %d > %d", len(meta.Asked), askedQuestionCap)
	}
	if len(meta.Touched) > touchedFileCap {
		t.Errorf("Touched grew past its cap: %d > %d", len(meta.Touched), touchedFileCap)
	}
}

// Touched is a ranking, and merging two ranked lists by position kept whichever
// paths came first. The file a session actually worked on hardest arrived in
// the tail and disappeared (#1304, defect 5).
func TestTheHardestWorkedFileSurvivesTheMerge(t *testing.T) {
	head := make([]model.Message, 0, 6)
	for _, p := range []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"} {
		head = append(head, model.Message{Role: roleFiles, Text: p})
	}
	var meta SessionMeta
	extendDerived(&meta, head)
	tail := make([]model.Message, 0, 12)
	for i := 0; i < 12; i++ {
		tail = append(tail, model.Message{Role: roleFiles, Text: "hot.go"})
	}
	extendDerived(&meta, tail)
	if len(meta.Touched) == 0 || meta.Touched[0] != "hot.go" {
		t.Errorf("the file worked on hardest is not first: %v", meta.Touched)
	}
}

// Growing the loser of an id collision moved the owner's row: its Words, its
// GaveUp, and the loser's files into its Touched — the wrong conversation
// surfacing in blame (#1304, defect 3).
func TestGrowingACollidingSessionLeavesTheOwnerAlone(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	owner := filepath.Join(claude, "-aaa")
	loser := filepath.Join(claude, "-zzz")
	for _, d := range []string{owner, loser} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	line := func(text string) string {
		return `{"type":"user","sessionId":"same","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(owner, "a.jsonl"), []byte(line("the owner session discusses the retry queue")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loser, "a.jsonl"), []byte(line("the other session discusses billing")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	was := before.Sessions["claude:same"]
	// The loser grows.
	grown := line("the other session discusses billing") + line("i gave up on the billing migration and reverted it")
	if err := os.WriteFile(filepath.Join(loser, "a.jsonl"), []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	after, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := after.Sessions["claude:same"]
	if now.GaveUp != was.GaveUp {
		t.Errorf("the loser's giving up was written onto the owner's row")
	}
	if now.Words != was.Words {
		t.Errorf("the owner's length moved with the loser's growth: %d then %d", was.Words, now.Words)
	}
}

// A session that repeats a line verbatim: resuming from the first copy would
// fold everything after it a second time on every re-delivery.
func TestARepeatedLineDoesNotRewindTheFold(t *testing.T) {
	same := model.Message{Role: "user", Text: "retry"}
	ms := []model.Message{same, {Role: "assistant", Text: "the backoff is capped at one second"}, same}
	var meta SessionMeta
	extendDerived(&meta, ms)
	first := meta.Words
	extendDerived(&meta, ms)
	if meta.Words != first {
		t.Errorf("a repeated line rewound the fold: %d words became %d", first, meta.Words)
	}
	if meta.Counted != len(ms) {
		t.Errorf("counted %d of %d messages", meta.Counted, len(ms))
	}
}
