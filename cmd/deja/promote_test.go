package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"io"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func promoteFixture(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "sess.jsonl"), "sess1234", []string{
		`{"type":"user","sessionId":"sess1234","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"how do we rotate the signing key without downtime"}}`,
		`{"type":"assistant","sessionId":"sess1234","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"double-publish the JWKS for one TTL, then swap the active kid"}]}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPromoteWritesNoteWithProvenanceAndState(t *testing.T) {
	dir := promoteFixture(t)
	var out bytes.Buffer
	if err := runPromote(dir, []string{"sess1234"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "promoted claude:sess1234 as accepted") {
		t.Fatalf("receipt wrong:\n%s", out.String())
	}
	b, err := os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"kind":"promoted"`, `"session":"claude:sess1234"`, `"state":"accepted"`, "signing key", "double-publish"} {
		if !strings.Contains(got, want) {
			t.Fatalf("note missing %q:\n%s", want, got)
		}
	}
}

func TestPromoteCorrectionAppends(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(dir, []string{"sess1234", "--state", "superseded", "--note", "replaced by the KMS flow"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), `"session":"claude:sess1234"`) != 2 {
		t.Fatalf("correction must append, not rewrite:\n%s", b)
	}
	ss, err := sources.ParseNotesFile(sources.NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("both entries must fold into one note session, got %d", len(ss))
	}
	if !strings.HasSuffix(ss[0].Title, "[superseded]") {
		t.Fatalf("title must carry the latest state, got %q", ss[0].Title)
	}
	// Full history, latest first: readers take the first messages, and after a
	// run of corrections that used to serve the oldest answer (#812).
	if len(ss[0].Messages) != 2 || !strings.Contains(ss[0].Messages[0].Text, "[superseded]") ||
		!strings.Contains(ss[0].Messages[1].Text, "[accepted]") {
		t.Fatalf("messages must keep the full history, newest first: %#v", ss[0].Messages)
	}
}

func TestPromoteRejectsBadStateAndNotes(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234", "--state", "maybe"}, &bytes.Buffer{}); err == nil {
		t.Fatal("bad state must fail")
	}
	if err := runPromote(dir, []string{"missing"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown prefix must fail")
	}
}

func TestPromoteExportsMarkdown(t *testing.T) {
	dir := promoteFixture(t)
	md := filepath.Join(t.TempDir(), "NOTES.md")
	if err := runPromote(dir, []string{"sess1234", "--to", md}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"- state: accepted", "- source: claude:sess1234", "double-publish"} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown missing %q:\n%s", want, got)
		}
	}
}

func TestPromotedNoteOutranksTranscriptInSearch(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234", "--note", "signing key rotation: double-publish JWKS for one TTL then swap kid"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := searchHits(t, dir, "signing key")
	if len(hits) < 2 {
		t.Fatalf("want note + transcript, got %d hits", len(hits))
	}
	if hits[0].Session.Harness != "deja" {
		t.Fatalf("promoted note must rank first, got %s:%s", hits[0].Session.Harness, hits[0].Session.ID)
	}
	if !strings.Contains(hits[0].Session.Title, "[accepted]") {
		t.Fatalf("state must show in the result title, got %q", hits[0].Session.Title)
	}
}

// A mark is not a verdict for life: the user who wrote "rejected" has to be
// able to say they judged it wrong. Re-marking accepted is the undo — the
// latest mark wins — and this pins both halves: the label and the demotion go,
// and the receipt says a mark was taken back rather than leaving the user to
// guess whether anything happened (#845).
func TestPromoteAcceptedTakesTheRejectedMarkBack(t *testing.T) {
	dir := promoteFixture(t)
	if err := runPromote(dir, []string{"sess1234", "--state", "rejected", "--note", "the JWKS double-publish broke old clients"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	hits := searchHits(t, dir, "signing key")
	attachLifecycles(hits)
	if len(hits) == 0 || hits[0].Lifecycle != "rejected" {
		t.Fatalf("rejected mark must land on the transcript hit: %#v", hits)
	}
	if !strings.Contains(lifecycleLine(hits[0]), "tried and rejected") {
		t.Fatalf("label missing: %q", lifecycleLine(hits[0]))
	}

	var out bytes.Buffer
	if err := runPromote(dir, []string{"sess1234", "--state", "accepted", "--note", "misjudged: old clients were the separate bug"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"takes back the rejected mark", "no longer labelled", "deja show deja-note-claude-sess1234"} {
		if !strings.Contains(got, want) {
			t.Fatalf("receipt must say the mark was taken back (%q):\n%s", want, got)
		}
	}

	hits = searchHits(t, dir, "signing key")
	attachLifecycles(hits)
	for _, h := range hits {
		if h.Lifecycle != "" {
			t.Fatalf("accepted must clear the mark, %s:%s still %q", h.Session.Harness, h.Session.ID, h.Lifecycle)
		}
	}
	if moved := demoteRejected(hits); moved != 0 {
		t.Fatalf("nothing may stay demoted after the mark is taken back, moved %d", moved)
	}
}

// The states that sound like an undo — none, clear, off — are all invalid, so
// the error for an unknown state is where a user learns which one is (#845).
func TestPromoteBadStateNamesTheUndo(t *testing.T) {
	dir := promoteFixture(t)
	err := runPromote(dir, []string{"sess1234", "--state", "none"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("bad state must fail")
	}
	if !strings.Contains(err.Error(), "`--state accepted` takes an earlier mark back") {
		t.Fatalf("error must name the undo: %v", err)
	}
}

// The undo is a rule, not a command, so the only place a user can learn it is
// the docs. This pins the README to the behavior the test above proves (#845).
func TestReadmeDocumentsTakingAMarkBack(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "--state accepted` takes the mark back") {
		t.Fatal("README must say how to take a rejected mark back")
	}
}

func searchHits(t *testing.T, dir, q string) []search.Hit {
	t.Helper()
	o := search.Options{Query: q, All: true}
	result, err := index.SearchWithRecoveryDetailed(dir, o, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	o.Tier = result.Tier
	hits, err := search.Run(result.Sessions, o)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func TestPromoteSurfacesConflicts(t *testing.T) {
	dir := promoteFixture(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "sess2.jsonl"), "sess9999", []string{
		`{"type":"user","sessionId":"sess9999","timestamp":"2026-01-05T03:04:05Z","message":{"role":"user","content":"the signing key rotation keeps breaking downstream verification"}}`,
		`{"type":"assistant","sessionId":"sess9999","timestamp":"2026-01-05T03:05:05Z","message":{"role":"assistant","content":[{"type":"text","text":"rotate the signing key monthly and pin the previous kid for one overlap window"}]}}`,
	})
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := runPromote(dir, []string{"sess1234", "--tag", "signing"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runPromote(dir, []string{"sess9999", "--tag", "signing"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "conflict: another accepted note") {
		t.Fatalf("conflict must surface at promote time:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "--state superseded") {
		t.Fatalf("resolution hint missing:\n%s", out.String())
	}
	// Superseding the older note dissolves the conflict for the next promote.
	if err := runPromote(dir, []string{"sess1234", "--state", "superseded"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runPromote(dir, []string{"sess9999", "--tag", "signing"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "conflict:") {
		t.Fatalf("superseded note must not conflict:\n%s", out.String())
	}
}

// The line has to stay quiet when there is nothing to take back: a first mark,
// a repeat of the same mark, or a session that never had one. Two mutants of
// markTakenBack shipped the sentence with an empty state in it and no test
// noticed (#845).
func TestPromoteSaysNothingWhenNoMarkWasTakenBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		prior sources.Lifecycle
	}{
		{"first mark of any kind", "accepted", sources.Lifecycle{}},
		{"accepted twice", "accepted", sources.Lifecycle{State: "accepted", At: time.Now()}},
		{"rejected after accepted", "rejected", sources.Lifecycle{State: "accepted", At: time.Now()}},
		{"rejected after rejected", "rejected", sources.Lifecycle{State: "rejected", At: time.Now()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := markTakenBack("claude:s1", tc.state, tc.prior); got != "" {
				t.Errorf("said %q with nothing to take back", got)
			}
		})
	}

	// And it does fire for every state an undo can reverse, naming that state.
	for _, prior := range []string{"rejected", "superseded", "stale"} {
		got := markTakenBack("claude:s1", "accepted", sources.Lifecycle{State: prior, At: time.Now()})
		if !strings.Contains(got, "takes back the "+prior+" mark") {
			t.Errorf("prior %s: %q", prior, got)
		}
	}
}
