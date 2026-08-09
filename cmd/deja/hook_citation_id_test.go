package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Credit said aloud is the only signal that a recall helped, and it was only
// linkable back to its session by title text — two sessions that open with the
// same question are common, so the credit landed on whichever one matched
// lexically. The citation carries the session id so the link is a fact.
func TestCitationLineCarriesTheSessionID(t *testing.T) {
	s := model.Session{
		ID:      "8f2c19ab77d40e6b5c31",
		Harness: "claude",
		Updated: time.Now().AddDate(0, 0, -2),
		Messages: []model.Message{
			{Role: "user", Text: "why does the reconciler double count refunds"},
		},
	}
	line := citationLine(s)
	if !strings.Contains(line, "deja:"+shortID(s.ID)) {
		t.Errorf("citation cannot be linked back to the session it came from: %q", line)
	}
	// The full id is not printed: the line is read aloud to the user, and a
	// twelve-character prefix already selects one session everywhere else.
	if strings.Contains(line, s.ID) {
		t.Errorf("citation spends the whole id on a line meant to be spoken: %q", line)
	}
}
