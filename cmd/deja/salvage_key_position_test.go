package main

import (
	"strings"
	"testing"
)

// The salvage path reads a payload the decoder could not — a cut megabyte of
// build log. It looked for each key by its first occurrence anywhere in the
// bytes, so a mention of the key inside a value stood in for the key itself
// (#2051).
func TestSalvageFindsTheKeyNotAMentionOfIt(t *testing.T) {
	// "stderr" is named inside stdout before the real field arrives. Reading
	// the mention, the scan gave up on stderr entirely and answered with
	// stdout — the wrong half of the payload, and the half without the error.
	// Unescaped on purpose, as above: escaping is what keeps this from
	// happening in a payload the decoder accepted.
	raw := `{"stdout":"nothing here, see "stderr" for the failure","stderr":"ld: symbol(s) not found"}`
	if got := salvageToolOutput(raw); !strings.Contains(got, "symbol(s) not found") {
		t.Errorf("a mention of the key stood in for the key: %q", got)
	}
}

// The salvage is scoped to what follows "tool_response" precisely so it cannot
// mine the command in tool_input. Scoping on the first occurrence, a command
// that names the key moved the scope inside itself.
func TestScopingIsNotMovedByTheCommandNamingTheKey(t *testing.T) {
	// Unescaped on purpose: this path exists for payloads the decoder could not
	// read, and a command spliced in without escaping is how one gets that way.
	raw := `{"tool_name":"Bash","tool_input":{"command":"grep -n "tool_response" hook.go && echo {"stderr": "mined"}"},` +
		`"tool_response":{"stderr":"ld: symbol(s) not found"}}`
	got := salvageToolOutput(after(raw, `"tool_response"`))
	if strings.Contains(got, "mined") {
		t.Errorf("the salvage mined the command it is scoped away from: %q", got)
	}
	if !strings.Contains(got, "symbol(s) not found") {
		t.Errorf("the real output was not salvaged: %q", got)
	}
}
