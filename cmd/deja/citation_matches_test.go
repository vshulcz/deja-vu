package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The sentence the agent is told to say aloud has to name the thing the user
// can see in the digest. Seen on a real kimi screen: the digest quoted a
// session about token rotation while the citation under it named "migration
// locked the table" — the first user line of the matched window, which after
// focusing a long session is whatever chatter began it.
func TestCitationNamesWhatWasMatched(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "work-02", Project: "goprojects/deja-vu",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "migration locked the table"},
			{Role: "assistant", Text: "it rewrote the whole table"},
			{Role: "user", Text: "how often does the deploy token for the billing gateway rotate?"},
			{Role: "assistant", Text: "every 47 days, pinned to the vault lease"},
		},
	}

	got := citationLine(s, []string{"deploy", "token", "rotate"})
	if !strings.Contains(got, "deploy token") {
		t.Fatalf("citation names something the digest never showed:\n%s", got)
	}
	if strings.Contains(got, "migration locked") {
		t.Fatalf("citation still takes the opening line:\n%s", got)
	}

	// Without terms — the session-start path — the opening line is still the
	// best summary there is.
	plain := citationLine(s, nil)
	if !strings.Contains(plain, "migration locked") {
		t.Fatalf("the no-terms fallback changed:\n%s", plain)
	}
}
