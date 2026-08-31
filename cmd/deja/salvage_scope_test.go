package main

import (
	"strings"
	"testing"
)

// salvage is the path for a payload the decoder could not read, and it finds
// its keys by text — so a command that mentions one takes the search with it.
// This is the composition the hook runs: scope to the tool's response, then
// pull a value out of it.
func salvaged(raw string) string { return salvageFromPayload(raw) }

// The scoping exists to keep salvage off the command, and it was done from the
// first occurrence of the key — which is the one inside the command whenever
// the command mentions it (#2051).
func TestSalvageDoesNotMineTheCommand(t *testing.T) {
	// Unescaped quotes inside the command, which is why the decoder failed and
	// salvage is running at all.
	raw := `{"tool_name":"Bash","tool_input":{"command":"echo '{"tool_response": {"stderr": "mined from the command"}}'"},` +
		`"tool_response":{"stderr":"connection refused on port 5432"}}`
	got := salvaged(raw)
	if strings.Contains(got, "mined from the command") {
		t.Errorf("salvage answered with the command's own text: %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("salvage missed the tool's stderr: %q", got)
	}
}

// And a mention that is not a key at all must not stop the search: the loop
// moved on to the next key rather than the next occurrence, so a real "stderr"
// further along was never reached.
func TestSalvageKeepsLookingPastAMentionOfTheKey(t *testing.T) {
	raw := `{"tool_response":{"command":"grep "stderr" build.log","stderr":"undefined: snorblefunc"}}`
	if got := salvaged(raw); !strings.Contains(got, "undefined: snorblefunc") {
		t.Errorf("a mention of the key hid the real one: %q", got)
	}
}
