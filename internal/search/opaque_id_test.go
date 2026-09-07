package search

import "testing"

// An id names one record and nothing else. Every rule in HasIdentifierTerm
// reads it as a term of art — long, mixed letters and digits — so a prompt
// carrying nothing but a task id cleared the bar that decides whether recall is
// worth opening the store for, and what it then matched was another record
// carrying an id of the same shape (#3156).
//
// The same test has to leave real vocabulary alone, which is most of what it
// costs to get right: `sha256sum` and `x86_64` are words people search for.
func TestOpaqueIDsAreNotIdentifiers(t *testing.T) {
	ids := []string{
		"br50mykp6",        // a Claude Code task id
		"942cbc1e",         // a uuid's first group
		"toolu_01A2b3C4d5", // a tool-call id behind its prefix
		"01k9x2m4p7q",      // a ulid fragment
		"7f3a9b2c1d4e",     // a hex digest fragment
		"a1b2c3d4e5f6",     // and one with every pair split
		"019f8c2a41b3",     // a uuidv7 prefix
		"ses1a2b3c4d5",     // a session id with a word-like head
		"deadbeef00ff11",   // hex that reads like a word at the start
	}
	for _, id := range ids {
		if HasIdentifierTerm([]string{id}) {
			t.Errorf("%q was taken as a term of art; it names one record", id)
		}
	}

	words := []string{
		"sha256sum", "iso8601", "x86_64", "utf8mb4", "base64", "oauth2",
		"pgbouncer", "http2", "libssl1", "error404page", "postgres14",
		"cve-2026-1234", "go1.22.4", "snake_case_name",
	}
	for _, w := range words {
		if !HasIdentifierTerm([]string{w}) {
			t.Errorf("%q stopped being an identifier; it is vocabulary, not an id", w)
		}
	}

	// An id alongside a real word leaves the word doing the work, so the
	// question still stands.
	if !HasIdentifierTerm([]string{"br50mykp6", "pgbouncer"}) {
		t.Error("an id next to a real term silenced the whole question")
	}
	// And a question that is only ids is not a question.
	if HasIdentifierTerm([]string{"br50mykp6", "942cbc1e"}) {
		t.Error("two ids read as a question")
	}
}
