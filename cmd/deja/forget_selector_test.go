package main

import (
	"strings"
	"testing"
)

// Every other command that takes a session id takes it bare. forget wants
// --session, which is right for the destructive one; what it said when it did
// not get it was that the id is an unknown flag, naming nothing the reader
// could do about it (#2656).
func TestForgetNamesTheFlagAnIdBelongsTo(t *testing.T) {
	for _, id := range []string{
		"imported-1aa758f985c5",
		"imported-…a758f985c5",
		"deadbeefcafe",
	} {
		t.Run(id, func(t *testing.T) {
			hermeticEnv(t)
			_, err := captureRun(t, "forget", id)
			if err == nil {
				t.Fatalf("forget accepted a bare id, which is the selector it deliberately does not take")
			}
			if strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("an id is not a flag: %v", err)
			}
			if !strings.Contains(err.Error(), "--session") {
				t.Fatalf("the message does not name the flag that would have worked: %v", err)
			}
			if !strings.Contains(err.Error(), id) {
				t.Fatalf("the message does not echo what was typed: %v", err)
			}
		})
	}
}

// A word that really is a flag keeps the flat refusal: naming --session there
// would send a reader after the wrong thing.
func TestForgetStillRefusesAnUnknownFlag(t *testing.T) {
	hermeticEnv(t)
	_, err := captureRun(t, "forget", "--sessions", "abc")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("a mistyped flag should still be called one, got %v", err)
	}
}
