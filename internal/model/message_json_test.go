package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The zero time marshals as "0001-01-01T00:00:00Z", which reads as a date
// rather than as the absence of one — a consumer sorting by it puts the message
// before everything that ever happened. Every surface deja prints already says
// "-" for it (#765, #2113).
func TestAMessageWithNoStampCarriesNoTimeField(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stamped, err := json.Marshal(Message{Role: "user", Text: "the pool was exhausted", Time: when})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stamped), `"time":"2026-01-02T03:04:05Z"`) {
		t.Fatalf("a stamped message lost its time: %s", stamped)
	}

	bare, err := json.Marshal(Message{Role: "assistant", Text: "raise the pool cap"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bare), "0001-01-01") {
		t.Errorf("a message deja could not date is published as the year one: %s", bare)
	}
	if strings.Contains(string(bare), `"time"`) {
		t.Errorf("the time field is present for a message that has none: %s", bare)
	}
	// The rest of the message is untouched, and it still round-trips.
	var back Message
	if err := json.Unmarshal(bare, &back); err != nil {
		t.Fatal(err)
	}
	if back.Role != "assistant" || back.Text != "raise the pool cap" || !back.Time.IsZero() {
		t.Errorf("the message did not survive the round trip: %#v", back)
	}
}
