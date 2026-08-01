package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The objection this answers, in the words it was raised in: "how do you know
// what was true, what was rejected, and what's obsolete?" deja recorded all
// three and then answered as though everything still stood — a session promoted
// as rejected came back through recall with its original conclusion and no
// trace of the reversal, because the correction only surfaced when its own
// wording happened to match the query.
func TestRecallSaysWhenADecisionWasReverted(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "payments", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"prepared statements fail behind pgbouncer"}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"We pinned pgx to 5.4.3 and left a note to revisit."}}`,
	})
	dir := index.DefaultDir()

	// Before anything is recorded, the transcript speaks for itself.
	before, err := recallText(dir, "prepared statements pgbouncer", "", 5, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "rejected") {
		t.Fatalf("nothing was promoted yet:\n%s", before)
	}

	if err := runPromote(dir, []string{"s1", "--state", "rejected", "--note", "the pin was reverted; pgbouncer 1.24 handles it natively"}, os.Stdout); err != nil {
		t.Fatal(err)
	}

	got, err := recallText(dir, "prepared statements pgbouncer", "", 5, 4096)
	if err != nil {
		t.Fatal(err)
	}
	// The state has to arrive with the hit on the transcript, not only as a
	// separate note that may or may not match the query.
	if !strings.Contains(got, "tried and rejected") {
		t.Fatalf("recall did not say the decision was rejected:\n%s", got)
	}
	if !strings.Contains(got, "pgbouncer 1.24 handles it natively") {
		t.Fatalf("recall dropped the correction:\n%s", got)
	}
	// And the original is still there: deja keeps history rather than deleting
	// it, so the reader can see what was decided as well as that it changed.
	if !strings.Contains(got, "pinned pgx to 5.4.3") {
		t.Fatalf("recall lost the original decision:\n%s", got)
	}
}

// A session promoted as accepted is not annotated: the marker means "this did
// not hold", so putting it on everything would make it noise.
func TestAcceptedSessionsAreNotAnnotated(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "infra", "s2.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"queue keeps losing messages"}}`,
		`{"type":"assistant","sessionId":"s2","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"Moved the job queue to Postgres advisory locks."}}`,
	})
	dir := index.DefaultDir()
	if err := runPromote(dir, []string{"s2", "--state", "accepted"}, os.Stdout); err != nil {
		t.Fatal(err)
	}
	got, err := recallText(dir, "queue losing messages", "", 5, 4096)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"tried and rejected", "replaced by a later decision", "marked stale"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("an accepted session was annotated with %q:\n%s", unwanted, got)
		}
	}
}

func TestLifecycleWordingSaysWhatHappened(t *testing.T) {
	for state, want := range map[string]string{
		"rejected":   "tried and rejected",
		"superseded": "a later decision replaced this",
		"stale":      "may no longer hold",
	} {
		got := lifecycleLine(hitWithLifecycle(state, "2026-07-29", "because reasons"))
		if !strings.Contains(got, want) {
			t.Fatalf("%s rendered as %q, want it to contain %q", state, got, want)
		}
		if !strings.Contains(got, "2026-07-29") || !strings.Contains(got, "because reasons") {
			t.Fatalf("%s lost its date or note: %q", state, got)
		}
	}
	if lifecycleLine(hitWithLifecycle("", "", "")) != "" {
		t.Fatal("a hit with no recorded state produced a line")
	}
}

func hitWithLifecycle(state, at, note string) search.Hit {
	return search.Hit{Lifecycle: state, LifecycleAt: at, LifecycleNote: note}
}

// rejected is the strongest statement someone can make about their own
// history, and it used to move nothing: the wrong answer stayed first while
// the decision that replaced it was labelled as superseded *by* the rejected
// one (#684).
func TestDemoteRejected(t *testing.T) {
	newer := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	older := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	hits := []search.Hit{
		{Session: model.Session{ID: "bad", Project: "p", Updated: newer}, Lifecycle: "rejected"},
		{Session: model.Session{ID: "good", Project: "p", Updated: older}, Superseded: "2026-07-28"},
	}
	_ = demoteRejected(hits)
	if hits[0].Session.ID != "good" || hits[1].Session.ID != "bad" {
		t.Fatalf("order = %s, %s", hits[0].Session.ID, hits[1].Session.ID)
	}
	// The surviving answer must not be labelled as replaced by the attempt
	// that was thrown out.
	if hits[0].Superseded != "" {
		t.Errorf("still labelled an earlier attempt: %q", hits[0].Superseded)
	}
	if hits[1].Superseded != "" {
		t.Errorf("the rejected hit gained a label: %q", hits[1].Superseded)
	}
}

// superseded and stale say "this was true once", which is still the best
// record of how the current answer was reached. Only rejected demotes.
func TestDemoteRejectedLeavesOtherStatesAlone(t *testing.T) {
	for _, state := range []string{"superseded", "stale", "accepted", ""} {
		hits := []search.Hit{
			{Session: model.Session{ID: "first", Project: "p"}, Lifecycle: state},
			{Session: model.Session{ID: "second", Project: "p"}},
		}
		_ = demoteRejected(hits)
		if hits[0].Session.ID != "first" {
			t.Errorf("state %q reordered the hits", state)
		}
	}
	// Everything rejected: there is nothing to promote above it, the ranking's
	// order stands, and a rejected hit keeps its own "earlier attempt" label —
	// being outranked by another rejected attempt is still what happened.
	day := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	hits := []search.Hit{
		{Session: model.Session{ID: "a", Project: "p"}, Lifecycle: "rejected", Superseded: "2026-07-28"},
		{Session: model.Session{ID: "b", Project: "p", Updated: day}, Lifecycle: "rejected"},
	}
	_ = demoteRejected(hits)
	if hits[0].Session.ID != "a" || hits[0].Superseded != "2026-07-28" {
		t.Errorf("all-rejected set was rewritten: %#v", hits)
	}
}

// Demotion is a reordering, not a re-ranking: everything the ranking decided
// among the hits that were not rejected has to survive it.
func TestDemoteRejectedKeepsTheRankingOrder(t *testing.T) {
	// Long enough that an unstable sort actually reorders: Go's pattern-defeating
	// quicksort falls back to insertion sort — which is stable — below twelve.
	var hits []search.Hit
	var want []string
	for i := 0; i < 24; i++ {
		id := fmt.Sprintf("h%02d", i)
		h := search.Hit{Session: model.Session{ID: id, Project: "p"}}
		if i%5 == 3 {
			h.Lifecycle = "rejected"
		} else {
			want = append(want, id)
		}
		hits = append(hits, h)
	}
	_ = demoteRejected(hits)
	var got []string
	for _, h := range hits {
		if h.Lifecycle != "rejected" {
			got = append(got, h.Session.ID)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ranking order lost:\n got %v\nwant %v", got, want)
	}
	for i, h := range hits {
		if h.Lifecycle == "rejected" && i < len(want) {
			t.Errorf("a rejected hit stayed at position %d", i)
		}
	}
}

// The label carries a date, not an identity: a rejected session in another
// project on the same day must not clear it.
func TestDemoteRejectedMatchesTheRightRival(t *testing.T) {
	newer := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	hits := []search.Hit{
		{Session: model.Session{ID: "other", Project: "elsewhere", Updated: newer}, Lifecycle: "rejected"},
		{Session: model.Session{ID: "mine", Project: "p"}, Superseded: "2026-07-28"},
	}
	_ = demoteRejected(hits)
	if hits[0].Session.ID != "mine" {
		t.Fatalf("order = %s", hits[0].Session.ID)
	}
	if hits[0].Superseded != "2026-07-28" {
		t.Errorf("cleared the label using another project's rejection")
	}
}

// Read top-down, an older session above a newer one with no explanation is
// what a broken ranking looks like; the reason sat four lines further down,
// attached to the result that moved (#694).
func TestDemotedNote(t *testing.T) {
	hits := []search.Hit{
		{Session: model.Session{ID: "bad", Project: "p"}, Lifecycle: "rejected"},
		{Session: model.Session{ID: "good", Project: "p"}},
	}
	moved := demoteRejected(hits)
	if moved == 0 {
		t.Fatal("nothing moved")
	}
	note := demotedNote(hits, moved)
	if !strings.Contains(note, "1 session you marked rejected is below") {
		t.Errorf("note = %q", note)
	}
	// Nothing moved, nothing to explain: a line on every result is noise.
	quiet := []search.Hit{
		{Session: model.Session{ID: "a", Project: "p"}},
		{Session: model.Session{ID: "b", Project: "p"}, Lifecycle: "rejected"},
	}
	if got := demotedNote(quiet, demoteRejected(quiet)); got != "" {
		t.Errorf("already in order, said %q", got)
	}
	// Plural, and counting the rejected hits rather than the moves.
	many := []search.Hit{
		{Session: model.Session{ID: "x", Project: "p"}, Lifecycle: "rejected"},
		{Session: model.Session{ID: "y", Project: "p"}, Lifecycle: "rejected"},
		{Session: model.Session{ID: "z", Project: "p"}},
	}
	if got := demotedNote(many, demoteRejected(many)); !strings.Contains(got, "2 sessions you marked rejected are below") {
		t.Errorf("plural note = %q", got)
	}
}
