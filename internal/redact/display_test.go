package redact

import (
	"strings"
	"testing"
)

// The bytes a terminal acts on rather than prints, each one measured arriving
// under a reader's own prompt from a transcript deja indexed.
func TestSafeForDisplayRemovesWhatATerminalActsOn(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"CSI repaints the screen", "\x1b[2J\x1b[H\x1b[31mred\x1b[0m", "red"},
		{"OSC sets the window title", "\x1b]0;pwned\x07rest", "rest"},
		{"OSC terminated by ST", "\x1b]0;pwned\x1b\\rest", "rest"},
		{"unterminated OSC eats no text after it", "\x1b]0;pwned", ""},
		{"bare escape", "a\x1bZb", "ab"},
		{"carriage return rewinds the line", "realtext\rSPOOFED", "realtext SPOOFED"},
		{"CRLF is a line ending, not a rewind", "one\r\ntwo", "one\ntwo"},
		{"backspace rubs out the word before it", "passwd\x08\x08\x08\x08\x08\x08SPOOF", "passwd      SPOOF"},
		{"bell", "bell\x07here", "bell here"},
		{"vertical tab and form feed", "a\x0bb\x0cc", "a b c"},
		{"bidi override reverses what is shown", "safe \u202egnp.exe\u202c tail", "safe  gnp.exe  tail"},
		{"C1 control", "a\u0090b", "a b"},
		{"newline and tab are layout", "keep\ttab\nand line", "keep\ttab\nand line"},
		{"a zero-width joiner is ordinary text", "family \u200d emoji", "family \u200d emoji"},
		{"plain text is returned byte for byte", "plain [brackets] and 0m digits", "plain [brackets] and 0m digits"},
	}
	for _, c := range cases {
		if got := SafeForDisplay(c.in); got != c.want {
			t.Errorf("%s: SafeForDisplay(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// Stripping must not take the text with it: a reader still has to be able to
// read what the session said.
func TestSafeForDisplayKeepsTheWords(t *testing.T) {
	in := "control probe \x1b[31mCSIPAYLOAD\x1b[0m \x1b]0;OSCPAYLOAD\x07 realtext\rSPOOFEDLINE"
	got := SafeForDisplay(in)
	for _, word := range []string{"control probe", "CSIPAYLOAD", "realtext", "SPOOFEDLINE"} {
		if !strings.Contains(got, word) {
			t.Fatalf("word %q was taken with the escapes: %q", word, got)
		}
	}
	if strings.Contains(got, "OSCPAYLOAD") {
		t.Fatalf("the OSC payload is part of the sequence and must go with it: %q", got)
	}
}
