package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The tool declares one payload argument, `q`, and each mode reads it as the
// thing that mode is about. Five declared strings cost more than the whole
// description did, and the model had to pick the right one after already
// picking the mode.
func TestQCarriesThePayloadForEveryMode(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		field string
	}{
		{"recall", "query"},
		{"context", "query"},
		{"blame", "path"},
		{"fix", "error"},
		{"how", "what"},
		{"remember", "text"},
	} {
		got := spreadQ(tc.mode, json.RawMessage(`{"mode":"`+tc.mode+`","q":"carried"}`))
		var args map[string]any
		if err := json.Unmarshal(got, &args); err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		if args[tc.field] != "carried" {
			t.Errorf("mode %s reads %q, and q did not reach it: %s", tc.mode, tc.field, got)
		}
	}
}

// The names q replaced are still accepted, so a client wired before this keeps
// working, and one that sends both is not overruled by the shorthand.
func TestTheOlderArgumentNamesStillWin(t *testing.T) {
	got := spreadQ("fix", json.RawMessage(`{"mode":"fix","q":"shorthand","error":"the real failing output"}`))
	if !strings.Contains(string(got), "the real failing output") || strings.Contains(string(got), `"error":"shorthand"`) {
		t.Errorf("q overwrote an argument the caller named itself: %s", got)
	}

	// An empty field is not a claim on the slot: a client that always sends the
	// key would otherwise turn q into a no-op.
	got = spreadQ("recall", json.RawMessage(`{"mode":"recall","q":"carried","query":""}`))
	if !strings.Contains(string(got), `"query":"carried"`) {
		t.Errorf("an empty query blocked q: %s", got)
	}
}

// A mode that reads no payload, and arguments that are not an object at all,
// have to pass through rather than fail here — the mode's own decoder owns
// those errors and says something the agent can act on.
func TestSpreadQLeavesWhatItDoesNotUnderstandAlone(t *testing.T) {
	for _, raw := range []string{`[1,2]`, `null`, ``, `{"mode":"recall"}`} {
		if got := string(spreadQ("recall", json.RawMessage(raw))); got != raw {
			t.Errorf("spreadQ(%q) rewrote it to %q", raw, got)
		}
	}
	if got := string(spreadQ("nonesuch", json.RawMessage(`{"q":"x"}`))); got != `{"q":"x"}` {
		t.Errorf("an unknown mode was rewritten: %s", got)
	}
}
