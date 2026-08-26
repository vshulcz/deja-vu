package search

import (
	"testing"
)

// Control bytes never reach the text deja hands an agent, and two things rest
// on that. The one this file was written for: a control byte is six bytes in
// JSON, so a digest carrying them would weigh twice what
// `usage.RecordSize` says — six bytes against the three it allows — and the log's own bound would be wrong.
// This holds SafeText and SafeLine to it; that every digest is built through
// one of them is about two dozen call sites and is not what this proves.
// The older one is why the stripping is there at all — an escape byte
// recolours a terminal and a carriage return rewinds a line.
func TestControlBytesDoNotSurviveIntoServedText(t *testing.T) {
	for _, c := range []struct{ name, in string }{
		{"an escape sequence", "before\x1b[31mafter"},
		{"a bell", "before\x07after"},
		{"a start-of-heading byte", "before\x01after"},
		{"a null byte", "before\x00after"},
		{"a carriage return", "before\rafter"},
		{"a vertical tab", "before\x0bafter"},
		{"a delete byte", "before\x7fafter"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := SafeText(c.in); hasControlByte(got) {
				t.Errorf("SafeText kept a control byte: %q", got)
			}
			if got := SafeLine(c.in); hasControlByte(got) {
				t.Errorf("SafeLine kept a control byte: %q", got)
			}
		})
	}
}

// hasControlByte ignores the two a transcript is made of: a newline separates
// turns, and a tab is ordinary in pasted output. Both cost two bytes in JSON,
// which is the bound `usage.RecordSize` is built on.
func hasControlByte(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
