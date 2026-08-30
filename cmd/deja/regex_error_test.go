package main

import (
	"strings"
	"testing"
)

// A mistyped pattern is read by someone already confused about their regex, and
// the sentence began with the name of an internal function: "deja: run: error
// parsing regexp…" (#2286).
func TestABadRegexIsReportedWithoutAnInternalName(t *testing.T) {
	withTempStores(t)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	_, err := captureRun(t, "search", "gate(way", "--re")
	if err == nil {
		t.Fatal("a broken regex was accepted")
	}
	msg := err.Error()
	// The premise: it is the regex it is complaining about, in deja's own
	// words rather than Go's (#1602).
	if !strings.Contains(msg, "is not a pattern deja can use") {
		t.Fatalf("the refusal is about something else: %q", msg)
	}
	if strings.Contains(msg, "parsing regexp") {
		t.Errorf("the refusal still carries Go's own prefix: %q", msg)
	}
	if strings.Contains(msg, "run:") {
		t.Errorf("the refusal carries an internal function name: %q", msg)
	}
	if !strings.Contains(msg, "--re") && !strings.Contains(msg, "pattern") {
		t.Errorf("the refusal does not say which input is wrong: %q", msg)
	}

	// A good pattern still searches.
	if _, err := captureRun(t, "search", "frobnicator", "--re"); err != nil {
		t.Errorf("a valid regex was refused: %v", err)
	}
}
