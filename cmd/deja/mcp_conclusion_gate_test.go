package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedMixedConclusions writes one long session that concluded two things: one
// about the subject of the query, and a newer one about unrelated work. A real
// session that ran for days looks like this, and "what this session concluded"
// reads the whole of it.
func seedMixedConclusions(t *testing.T, tail string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(text, hour string) string {
		return `{"type":"assistant","message":{"role":"assistant","content":"` + text +
			`"},"timestamp":"2026-08-02T` + hour + `:00:00Z","sessionId":"csa","cwd":"/proj"}` + "\n"
	}
	var b strings.Builder
	for i := range 20 {
		b.WriteString(line(strings.Repeat("flimsyshard retry cap discussion ", 8), "0"+string(rune('0'+i%10))))
	}
	b.WriteString(line("so we decided to cap flimsyshard retries at three and log the fourth, "+
		strings.Repeat("because the queue drains slower than it fills ", 6), "11"))
	// Newer, and about something else entirely: this is what the list shows
	// first when it is only going by age.
	b.WriteString(line("in the end we moved the invoice mailer onto the nightly queue, "+
		strings.Repeat("so the morning batch stops competing with it ", 6), "12"))
	b.WriteString(line("the cause was a stale etag on the partner avatars, "+
		strings.Repeat("and the proxy kept serving it for an hour ", 6), "13"))
	if tail != "" {
		b.WriteString(line(tail, "14"))
	}
	if err := os.WriteFile(filepath.Join(store, "csa.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// seedUnrelatedConclusions writes a session that matches the question on its
// filler and concluded nothing about it — the shape a long session has when a
// question reaches it through one passing mention.
func seedUnrelatedConclusions(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(text, hour string) string {
		return `{"type":"assistant","message":{"role":"assistant","content":"` + text +
			`"},"timestamp":"2026-08-02T` + hour + `:00:00Z","sessionId":"csb","cwd":"/proj"}` + "\n"
	}
	var b strings.Builder
	for i := range 20 {
		b.WriteString(line(strings.Repeat("glimwrax polling window notes ", 8), "0"+string(rune('0'+i%10))))
	}
	b.WriteString(line("in the end we moved the invoice mailer onto the nightly queue, "+
		strings.Repeat("so the morning batch stops competing with it ", 6), "11"))
	b.WriteString(line("the cause was a stale etag on the partner avatars, "+
		strings.Repeat("and the proxy kept serving it for an hour ", 6), "12"))
	if err := os.WriteFile(filepath.Join(store, "csb.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Conclusions are read off the whole session and sorted by age, so a session
// that worked on three things served its newest three to every question about
// any of them: over sixteen recall calls on a real store, 42% of the conclusion
// lines shared no word with the query or with the excerpts under them.
func TestTheConclusionsListLeavesOutOtherWork(t *testing.T) {
	dir := seedMixedConclusions(t, "")
	text, _, _, _, err := recallTextResult(dir, "flimsyshard retry cap", "", 0, 0, 4096-recallFrameOverhead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "cap flimsyshard retries") {
		t.Errorf("the conclusion about what was asked is missing:\n%s", text)
	}
	for _, other := range []string{"invoice mailer", "partner avatars"} {
		if strings.Contains(text, other) {
			t.Errorf("a conclusion about other work (%q) was served as this session's conclusion:\n%s", other, text)
		}
	}
}

// And when a session concluded nothing the gate can tie to the question, it
// still offers one line. A conclusion nobody asked for can be the one that
// helps, and the store is full of sessions whose words are in another language
// than the query — withholding all of them would cost more than it saves.
func TestASessionWithNoRelatedConclusionStillOffersOne(t *testing.T) {
	dir := seedUnrelatedConclusions(t)
	text, _, _, _, err := recallTextResult(dir, "glimwrax polling window", "", 0, 0, 4096-recallFrameOverhead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "what this session concluded") {
		t.Errorf("the session offered no conclusion at all:\n%s", text)
	}
	shown := strings.Count(text, "\n  → ")
	if shown != 1 {
		t.Errorf("offered %d conclusion lines; an unrelated session gets one, not a list:\n%s", shown, text)
	}
	// And it is offered rather than claimed: the label is what an agent reads as
	// the claim, so a line about the session's other work must not arrive under
	// one that says it answers the question.
	if !strings.Contains(text, "concluded, about its own work:") {
		t.Errorf("an unrelated conclusion was labelled as this question's:\n%s", text)
	}
}

// And the label stays plain when the conclusion is the question's: the hedge is
// for the fallback, not for every hit.
func TestARelatedConclusionIsNotHedged(t *testing.T) {
	dir := seedMixedConclusions(t, "")
	text, _, _, _, err := recallTextResult(dir, "flimsyshard retry cap", "", 0, 0, 4096-recallFrameOverhead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "what this session concluded:") {
		t.Errorf("the plain label is missing:\n%s", text)
	}
	if strings.Contains(text, "about its own work") {
		t.Errorf("a conclusion about the question was hedged:\n%s", text)
	}
}
