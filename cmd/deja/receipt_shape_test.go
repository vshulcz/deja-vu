package main

import (
	"strings"
	"testing"
)

// The receipt is read at a glance, and several hosts show it in a toast about
// fifty columns wide. Driven through a real opencode TUI, the old 130-character
// line wrapped to three rows over the conversation and split "2.4 KB" across
// two of them. These pin the shape that survives that: the claim first, the
// numbers last, and the explanation only while it is still news.
func TestReceiptShape(t *testing.T) {
	const first = "deja: recalled 3 prior sessions from this project — the agent starts already knowing them · 2.4 KB of context"
	const later = "deja: recalled 3 prior sessions from this project · today: 2 recalls · 2.4 KB of context"

	for _, line := range []string{first, later} {
		if len(line) > 120 {
			t.Errorf("receipt is %d chars; a toast wraps it into the conversation:\n%s", len(line), line)
		}
		if !strings.HasPrefix(line, "deja: recalled ") {
			t.Errorf("the claim must lead: %q", line)
		}
		if !strings.HasSuffix(line, "of context") {
			t.Errorf("the size must trail, where a wrap costs least: %q", line)
		}
	}
	// Teaching once: the clause explaining what a recall is has no place next
	// to a running count of today's recalls.
	if !strings.Contains(first, "starts already knowing") {
		t.Error("the first receipt of the day must still explain itself")
	}
	if strings.Contains(later, "starts already knowing") {
		t.Error("the explanation repeats after it has been read")
	}
}
