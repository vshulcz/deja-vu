package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// An agent that sends `"limit":"five"` was told "arguments must be an object",
// which is about the one part of the call that was correct. mcpNumber already
// says what it wanted; the decoder threw that away.
func TestANumericArgumentThatIsNotANumberSaysSo(t *testing.T) {
	var a struct {
		Query string    `json:"query"`
		Limit mcpNumber `json:"limit"`
	}
	err := decodeToolArgs("recall", json.RawMessage(`{"query":"exporter","limit":"five"}`), &a)
	if err == nil {
		t.Fatal("a limit of \"five\" was accepted")
	}
	if strings.Contains(err.Error(), "must be an object") {
		t.Errorf("the error blames the argument object: %v", err)
	}
	if !strings.Contains(err.Error(), "number") || !strings.Contains(err.Error(), "five") {
		t.Errorf("the error says neither what was wanted nor what came: %v", err)
	}

	// A numeric string is still a number, the way hosts that stringify send it.
	a.Limit = 0
	if err := decodeToolArgs("recall", json.RawMessage(`{"query":"exporter","limit":"3"}`), &a); err != nil {
		t.Fatalf("a stringified number was refused: %v", err)
	}
	if a.Limit != 3 {
		t.Errorf("limit = %v, want 3", a.Limit)
	}

	// And arguments that are genuinely not an object keep the old sentence.
	err = decodeToolArgs("recall", json.RawMessage(`["exporter"]`), &a)
	if err == nil || !strings.Contains(err.Error(), "must be an object") {
		t.Errorf("a JSON array said: %v", err)
	}

	// A wrong type on a plain field keeps naming the field.
	err = decodeToolArgs("recall", json.RawMessage(`{"query":["exporter"]}`), &a)
	if err == nil || !strings.Contains(err.Error(), `"query" must be a string`) {
		t.Errorf("a list query said: %v", err)
	}
}
