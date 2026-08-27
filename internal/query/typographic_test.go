package query

import (
	"reflect"
	"testing"
)

// A query pasted from a chat client, a word processor or an agent's own prose
// carries typographic punctuation, and the ASCII trim left it glued to the
// word — so the token matched nothing while the index, which splits on
// everything that is not a letter, digit, `_` or `-`, held the plain word
// (#2117).
func TestTokensTrimTypographicPunctuation(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{`retry`, []string{"retry"}},
		{`"retry"`, []string{"retry"}},
		{"“retry”", []string{"retry"}},
		{"«retry»", []string{"retry"}},
		{"‘retry’", []string{"retry"}},
		{"—retry—", []string{"retry"}},
		{"the retry budget…", []string{"the", "retry", "budget"}},
		// A curly apostrophe is punctuation outside ASCII, so it breaks the
		// word; "s" is one byte and drops out.
		{"retry’s budget", []string{"retry", "budget"}},
		// The ASCII one is trimmed at the ends only, as it always has been —
		// a regex query depends on ASCII staying where the reader put it.
		{"retry's budget", []string{"retry's", "budget"}},
		{"dad|gift", []string{"dad|gift"}},
		// What the index keeps inside a token stays inside it here.
		{"retry-budget retry_budget", []string{"retry-budget", "retry_budget"}},
		// A flag is not a query word — parseSearch takes those before this is
		// reached — and `-` is part of a token either way.
		{"--re", []string{"--re"}},
	} {
		if got := Tokens(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokens(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
