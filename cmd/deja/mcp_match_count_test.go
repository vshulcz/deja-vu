package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// manySessionStore writes n sessions that all carry one common word and one
// that carries a word nothing else has.
func manySessionStore(t *testing.T, n int) string {
	t.Helper()
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"the pipeline run %d stalled on retry"},"timestamp":"2026-08-02T10:00:0%dZ","sessionId":"s%d","cwd":"/work"}`, i, i%10, i) + "\n"
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rare := `{"type":"user","message":{"role":"user","content":"the quibblesnatch parser rejects empty frames"},"timestamp":"2026-08-02T11:00:00Z","sessionId":"rare","cwd":"/work"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "rare.jsonl"), []byte(rare), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// "(5 match(es))" means five came back and reads as five exist. An agent had no
// way to tell "five sessions in the whole store discussed this" — worth acting
// on — from "the store matched, here are five of them" (#1308).
func TestRecallSaysHowManyMatchedNotJustHowManyCameBack(t *testing.T) {
	dir := manySessionStore(t, 40)
	text, _, _, _, err := recallTextResult(dir, "pipeline", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	// 40 sessions carry "pipeline"; the 41st carries neither.
	if !strings.Contains(text, "(5 of 40 matched)") {
		t.Errorf("the answer does not say how many sessions matched:\n%s", firstLines(text, 4))
	}
	// And the follow-up line agrees with it.
	if !strings.Contains(text, "35 more match(es) — call recall again with offset=5.") {
		t.Errorf("the numbers on the two lines do not add up:\n%s", text)
	}
}

// A precise hit still reads as precise: everything that matched came back, so
// there is no sample to warn about.
func TestRecallKeepsThePlainCountWhenNothingWasHeldBack(t *testing.T) {
	dir := manySessionStore(t, 40)
	text, _, _, _, err := recallTextResult(dir, "quibblesnatch", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "matched)") {
		t.Errorf("a complete answer was reported as a sample:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "match(es)") {
		t.Errorf("the count line went missing:\n%s", firstLines(text, 4))
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// The relevance tier answers with the nearest sessions when nothing matched,
// and the line above the count already says so. Calling those matches would
// contradict it on the same screen.
func TestRelevanceHitsAreNotCalledMatches(t *testing.T) {
	dir := manySessionStore(t, 40)
	// The query mixes words from both halves of the fixture: "parser rejects
	// frames" appears only in the rare session, "pipeline stalled" only in the
	// forty bulk ones. No session can hold all of them, so exact matching has
	// nothing to return and the relevance tier answers — a property of how the
	// store is built rather than of how ranking happens to score today.
	text, _, _, _, err := recallTextResult(dir, "parser rejects frames the pipeline stalled", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	// A fixture that stops reaching the tier makes this test guard nothing,
	// and skipping there hides that: the query above stopped reaching it once
	// already, and the test went quiet instead of saying so.
	// The sentinel is the tier's own words, and they changed when the line
	// stopped reading as "here is what I found about it" (#2074).
	if !strings.Contains(text, "nearest by wording") {
		t.Fatalf("the fixture no longer reaches the relevance tier, so this test guards nothing:\n%s", firstLines(text, 3))
	}
	if strings.Contains(text, "matched)") {
		t.Errorf("relevance-tier sessions were reported as matches:\n%s", firstLines(text, 4))
	}
	if !strings.Contains(text, "ranked)") {
		t.Errorf("the count line does not say what the number is:\n%s", firstLines(text, 4))
	}
}

// The budget can stop the loop before the limit does, and then the follow-up
// line has to count from what was served rather than from the limit — the
// agent asks for offset=served, so the arithmetic is what it navigates by.
func TestTheFollowUpCountMatchesWhatWasServed(t *testing.T) {
	dir := manySessionStore(t, 40)
	text, served, _, _, err := recallTextResult(dir, "pipeline", "", 15, 0, 900)
	if err != nil {
		t.Fatal(err)
	}
	// Same rule as above. This one is the more fragile of the two — it holds
	// only while 900 bytes is smaller than fifteen served sessions — so it
	// fails loudly rather than skipping: a message format that shrinks enough
	// to fit them all should stop this test, not quiet it.
	if served >= 15 {
		t.Fatalf("the budget no longer cuts this answer short (served %d of 15), so this test guards nothing", served)
	}
	want := fmt.Sprintf("%d more match(es) — call recall again with offset=%d.", 40-served, served)
	if !strings.Contains(text, want) {
		t.Errorf("expected %q in:\n%s", want, text)
	}
	// And the header counts the same thing: it used to promise the page the
	// server prepared, so fifteen were announced and nine arrived.
	head := fmt.Sprintf("(%d of 40 matched)", served)
	if !strings.Contains(text, head) {
		t.Errorf("expected %q in:\n%s", head, firstLines(text, 2))
	}
}
