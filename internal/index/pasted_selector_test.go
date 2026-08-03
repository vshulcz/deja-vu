package index

import "testing"

// What a paste carries around an id: a chat wraps it in quotes or backticks,
// and deja's own screens print `harness:id` (#921).
func TestPastedSelectorStripsWhatAPasteCarries(t *testing.T) {
	const id = "a1b2c3d4-1111-4000-8000-d6e7f8a9b0c1"
	for _, tc := range []struct{ in, want string }{
		{id, id},
		{"  " + id + "  ", id},
		{`"` + id + `"`, id},
		{"'" + id + "'", id},
		{"`" + id + "`", id},
		{"“" + id + "”", id},
		{"‘" + id + "’", id},
		{"`\"" + id + "\"`", id},
		// Not a wrapping pair: a quote on one side is part of what was typed.
		{`"` + id, `"` + id},
		{"", ""},
		{`"`, `"`},
	} {
		if got := PastedSelector(tc.in); got != tc.want {
			t.Errorf("PastedSelector(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitSelectorTakesOnlyAHarnessPrefix(t *testing.T) {
	for _, tc := range []struct{ in, harness, id string }{
		{"claude:abc", "claude", "abc"},
		{"cursor-cli:abc", "cursor-cli", "abc"},
		{"abc", "", "abc"},
		// A colon at either end names no harness and no id.
		{":abc", "", ":abc"},
		{"claude:", "", "claude:"},
		// A left side that merely looks harness-shaped is split too — the
		// callers try the whole string first and then require the harness to
		// equal the session's own, so a split like this simply matches
		// nothing.
		{"2026-08-03T10:00:00Z", "2026-08-03T10", "00:00Z"},
		// A space is not part of any harness name.
		{"a b:c", "", "a b:c"},
	} {
		h, id := splitSelector(tc.in)
		if h != tc.harness || id != tc.id {
			t.Errorf("splitSelector(%q) = (%q, %q), want (%q, %q)", tc.in, h, id, tc.harness, tc.id)
		}
	}
}

// A session answers to its id, to the elided form, and to the harness:id shape
// — but only under its own harness.
func TestSelectorMatchesHonoursTheHarness(t *testing.T) {
	meta := SessionMeta{ID: "a1b2c3d4-1111", Harness: "claude"}
	for _, sel := range []string{"a1b2c3d4-1111", "a1b2", "claude:a1b2", "CLAUDE:a1b2c3d4-1111"} {
		if !selectorMatches(meta, sel) {
			t.Errorf("%q did not match", sel)
		}
	}
	for _, sel := range []string{"codex:a1b2", "b1b2", "claude:zzz"} {
		if selectorMatches(meta, sel) {
			t.Errorf("%q matched and should not have", sel)
		}
	}
}
