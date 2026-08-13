package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func longSession(t *testing.T) model.Session {
	t.Helper()
	msgs := []model.Message{
		{Role: "user", Text: "let's start on the installer today"},
		{Role: "assistant", Text: "opened the installer; nothing surprising yet"},
	}
	// The region that answers, buried in the middle where a first-three-lines
	// digest never reaches it.
	msgs = append(msgs,
		model.Message{Role: "user", Text: "why is the provider gate so slow"},
		model.Message{Role: "assistant", Text: "unrelated preamble about the installer\nthe gate matched every word containing gh, so the regex ran on nearly every message"},
	)
	for i := 0; i < 6; i++ {
		msgs = append(msgs, model.Message{Role: "assistant", Text: "later chatter about packaging"})
	}
	return model.Session{
		Harness: "claude", ID: "long", Project: "goprojects/deja-vu",
		Updated: time.Now(), Messages: msgs,
	}
}

// A session is quoted where it matched, not where it opened. Without terms the
// digest keeps its session-start behaviour, because there is no question yet.
func TestDigestQuotesTheMatchingLines(t *testing.T) {
	s := longSession(t)

	plain := AutoRecallDigest([]model.Session{s}, 2000)
	if !strings.Contains(plain, "installer today") {
		t.Fatalf("without terms the digest must still open the session:\n%s", plain)
	}

	focused := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"provider", "gate"})
	if !strings.Contains(focused, "provider gate") {
		t.Fatalf("the matching question was not quoted:\n%s", focused)
	}
	if !strings.Contains(focused, "containing gh") {
		t.Fatalf("the answering line was not quoted:\n%s", focused)
	}
	if strings.Contains(focused, "unrelated preamble") {
		t.Fatalf("a message was quoted at its opening rather than where it matched:\n%s", focused)
	}
}

// A session that ranked through a fold carries none of the raw terms, and must
// still show something rather than nothing.
func TestDigestFallsBackWhenNothingMatchesLiterally(t *testing.T) {
	s := longSession(t)
	got := AutoRecallDigestFor([]model.Session{s}, 2000, []string{"zzzabsent"})
	if !strings.Contains(got, "installer today") {
		t.Fatalf("fallback to the session opening was lost:\n%s", got)
	}
}

func TestTermHits(t *testing.T) {
	if got := termHits("The Provider Gate is slow", []string{"provider", "gate", "absent"}); got != 2 {
		t.Fatalf("termHits = %d, want 2", got)
	}
	if got := termHits("anything", nil); got != 0 {
		t.Fatalf("no terms must score nothing, got %d", got)
	}
	if got := termHits("anything", []string{""}); got != 0 {
		t.Fatalf("an empty term must not match, got %d", got)
	}
}

func TestDensestLinePicksWhereItAnswers(t *testing.T) {
	text := "opening line\nthe provider gate matched every word\ntrailing line"
	line, hits := densestLine(text, []string{"provider", "gate"})
	if hits != 2 || !strings.Contains(line, "matched every word") {
		t.Fatalf("densestLine = %q (%d hits)", line, hits)
	}
	if line, hits := densestLine("nothing here", []string{"absent"}); hits != 0 || line != "nothing here" {
		t.Fatalf("a text with no hits must come back whole: %q (%d)", line, hits)
	}
	if _, hits := densestLine("   \n  \n", []string{"absent"}); hits != 0 {
		t.Fatalf("blank text must score nothing, got %d", hits)
	}
}
