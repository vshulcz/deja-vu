package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
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
