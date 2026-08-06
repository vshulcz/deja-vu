package redact

import (
	"strings"
	"unicode"
)

// A transcript is text someone else's tool wrote, and deja prints it back into
// a terminal, a markdown file meant to be sent, and an agent's context. The
// bytes a terminal acts on rather than prints therefore arrive under the
// reader's own prompt: an escape repaints the screen, a carriage return rewinds
// the line and overwrites what was already there, a backspace rubs out the
// word before it, an OSC sets the window title, a bidi override reverses the
// order of what is displayed against the order of what is stored.
//
// Each surface used to bring its own filter and each covered a different
// subset — the status bar stripped everything, snippets stripped CSI only, and
// `show`, `share` and `last` stripped nothing. This is the one rule for all of
// them.
//
// Newline and tab are kept: they are layout, they are what code in a transcript
// is made of, and no terminal acts on them beyond moving on.

// bidiControls are the format characters that make displayed order disagree
// with stored order — the Trojan Source set. Other Cf runes are left alone
// because they are ordinary text: a zero-width joiner holds an emoji together
// and a soft hyphen is a hyphenation hint.
func isBidiControl(r rune) bool {
	switch r {
	case '‪', '‫', '‬', '‭', '‮', // embeddings and overrides
		'⁦', '⁧', '⁨', '⁩', // isolates
		'‎', '‏': // left-to-right and right-to-left marks
		return true
	}
	return false
}

// SafeForDisplay removes escape sequences and replaces every other control
// character with a space, so what a reader sees is what the transcript holds.
// Newline and tab survive unchanged.
func SafeForDisplay(s string) string {
	if !needsDisplaySafety(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == 0x1b {
			i = skipEscape(runes, i)
			continue
		}
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		// A carriage return before a newline is a line ending, not a rewind.
		if r == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
			continue
		}
		if isBidiControl(r) || unicode.IsControl(r) {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// skipEscape returns the index of the last rune belonging to the escape
// sequence that starts at i, so the caller's loop resumes after it.
func skipEscape(runes []rune, i int) int {
	if i+1 >= len(runes) {
		return i
	}
	switch runes[i+1] {
	case '[': // CSI: parameters, then one final byte in @-~
		for j := i + 2; j < len(runes); j++ {
			if runes[j] >= '@' && runes[j] <= '~' {
				return j
			}
		}
		return len(runes)
	case ']': // OSC: runs to a BEL or to ST (ESC \)
		for j := i + 2; j < len(runes); j++ {
			if runes[j] == 0x07 {
				return j
			}
			if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
				return j + 1
			}
		}
		return len(runes)
	}
	// Anything else is a two-rune escape.
	return i + 1
}

// needsDisplaySafety keeps the common path allocation-free: almost every line
// deja prints holds nothing to strip.
func needsDisplaySafety(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || isBidiControl(r) {
			return true
		}
	}
	return false
}
