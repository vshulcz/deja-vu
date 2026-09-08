package index

import "testing"

// Cursor writes `<timestamp>…</timestamp>` above `<user_query>…</user_query>`,
// and removing the one and unwrapping the other each left the newline that
// separated them, so every Cursor turn on this machine's store was indexed
// with a blank line ahead of the question (#3357).
func TestStripSelfRecallLeavesNoBlankLineWhereABlockWas(t *testing.T) {
	in := "<timestamp>Sunday, Jul 26, 2026, 1:06 PM (UTC+3)</timestamp>\n<user_query>\nwhy does the retry loop drop the last attempt\n</user_query>"
	if got := stripSelfRecall(in); got != "why does the retry loop drop the last attempt" {
		t.Errorf("stripSelfRecall = %q, want the question with nothing around it", got)
	}
	// A block in the middle keeps the paragraph break around it, and the words
	// on both sides stay whole.
	mid := "first line\n\n<timestamp>Sunday, Jul 26, 2026, 1:06 PM</timestamp>\n\nsecond line"
	if got := stripSelfRecall(mid); got != "first line\n\n<timestamp>Sunday, Jul 26, 2026, 1:06 PM</timestamp>\n\nsecond line" {
		t.Errorf("stripSelfRecall = %q — a stamp that is not the head of the message is prose", got)
	}
}

// And nothing is trimmed from a message the cleaning did not touch: a code
// block that opens and closes with a blank line keeps both (review of #3357).
func TestStripSelfRecallLeavesAnUntouchedMessageAlone(t *testing.T) {
	in := "\n\n```\n\nfunc foo() {}\n```\n\n"
	if got := stripSelfRecall(in); got != in {
		t.Errorf("stripSelfRecall = %q, want the message unchanged — nothing was stripped from it", got)
	}
}

// The trim belongs where the removal happened. Trimming the whole message
// instead ate what an author wrote at the far end — a blank line after their
// pasted diff — and made a message's edges depend on whether a marker fired
// somewhere else entirely (second review of #3357).
func TestStripSelfRecallTrimsOnlyWhereItCut(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"<timestamp>Sunday, Jul 26, 2026, 1:06 PM</timestamp>\n\nhere is my diff:\n\n```\n+foo\n```\n\n",
			"here is my diff:\n\n```\n+foo\n```\n\n",
		},
		{
			"\n\nfirst paragraph\n\n<system-reminder>noise</system-reminder>\n\nsecond paragraph\n\n",
			"\n\nfirst paragraph\n\n\n\nsecond paragraph\n\n",
		},
	}
	for _, c := range cases {
		if got := stripSelfRecall(c.in); got != c.want {
			t.Errorf("stripSelfRecall(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
