package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A `/loop 1m …` sends the same text every minute, and the per-session
// cooldowns are counted per session shown — so an identical prompt walked one
// session further down the ranking on every tick: 25 blocks from 21 different
// sessions in one evening, none of them the loop's subject (#3189).
func TestTheSameQuestionIsAnsweredOnce(t *testing.T) {
	dir := t.TempDir() + "/index"
	terms := []string{"zebraquux", "fetcher", "timeout"}
	key := askKey(terms)

	if askedRecently(dir, "sess-1", key, repeatAskWindow) {
		t.Fatal("nothing has been asked yet")
	}
	rememberInjectedIDsFor(dir, "sess-1", "", []string{key})
	if !askedRecently(dir, "sess-1", key, repeatAskWindow) {
		t.Error("the same question from the same reader is answered again")
	}
	// The order of the terms is the ranking's business, not the reader's.
	if !askedRecently(dir, "sess-1", askKey([]string{"timeout", "zebraquux", "fetcher"}), repeatAskWindow) {
		t.Error("the same terms in another order read as another question")
	}
	// Another question, and another reader, are not this one.
	if askedRecently(dir, "sess-1", askKey([]string{"quokkabloom", "retry"}), repeatAskWindow) {
		t.Error("a different question was taken for the one just answered")
	}
	if askedRecently(dir, "sess-2", key, repeatAskWindow) {
		t.Error("one reader's answer silenced another reader")
	}
	// The window is what ends the silence: the same subject a day later is a
	// question again.
	if askedRecently(dir, "sess-1", key, time.Nanosecond) {
		t.Error("the window is not honoured")
	}
}

// The row has to carry a time, or the window cannot end.
func TestTheAnsweredQuestionIsStamped(t *testing.T) {
	dir := t.TempDir() + "/index"
	rememberInjectedIDsFor(dir, "sess-1", "", []string{askKey([]string{"zebraquux"})})
	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(strings.TrimSpace(string(b)))
	if len(parts) < 3 {
		t.Fatalf("row has no timestamp: %q", b)
	}
	if _, err := time.Parse(time.RFC3339, parts[2]); err != nil {
		t.Errorf("stamp %q does not parse: %v", parts[2], err)
	}
	if !strings.HasPrefix(parts[1], "ask:") {
		t.Errorf("the question key %q could be taken for a session id", parts[1])
	}
	// Wide enough that two questions do not share a key: a narrow one silences
	// a question nobody asked, which is the failure the reader cannot see.
	if got := len(strings.TrimPrefix(parts[1], "ask:")); got != 16 {
		t.Errorf("question key is %d hex digits, want 16", got)
	}
}

// Compaction takes the block off the screen, so the question may be answered
// again — the same rule the block fingerprint follows.
func TestCompactionClearsTheAnsweredQuestion(t *testing.T) {
	dir := t.TempDir() + "/index"
	key := askKey([]string{"zebraquux", "fetcher"})
	rememberInjectedIDsFor(dir, "sess-1", "", []string{key})
	forgetInjected(dir, "sess-1")
	if askedRecently(dir, "sess-1", key, repeatAskWindow) {
		t.Error("the question stayed answered after the answer was compacted away")
	}
}
