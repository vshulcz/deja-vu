package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The line under a share tells the user how much was scrubbed, and it read
// "0 secrets masked" on a document visibly full of markers: secrets are
// redacted at index time, so the pass share runs over already-clean text
// replaces nothing.
func TestShareCountsSecretsRedactedEarlier(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	var out bytes.Buffer
	printSanitized(&out, "key [redacted:openai-key] and password [redacted:credential]\n")
	_ = w.Close()
	os.Stderr = old
	var msg bytes.Buffer
	if _, err := msg.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.String(), "2 secrets masked") {
		t.Fatalf("count is wrong: %q", msg.String())
	}
}

func TestShareCountsZeroWhenNothingWasRedacted(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	var out bytes.Buffer
	printSanitized(&out, "nothing sensitive here at all\n")
	_ = w.Close()
	os.Stderr = old
	var msg bytes.Buffer
	if _, err := msg.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.String(), "0 secrets masked") {
		t.Fatalf("clean document reported as masked: %q", msg.String())
	}
}
