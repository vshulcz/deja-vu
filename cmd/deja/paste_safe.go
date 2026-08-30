package main

import (
	"fmt"
	"strings"
)

// pasteSafe renders a value the reader is meant to paste back into a shell.
//
// The tombstone hint is the case that forced it: the id is the argument of the
// command that lifts the tombstone, so sanitising it hands over a command that
// names nothing — and printing it raw sends an escape byte from a project name
// straight to the terminal (#1794, the half #1792 left). ANSI-C quoting is
// both at once: the bytes are visible as `\e`, `\t`, `\x00`, and bash and zsh
// turn them back into the same bytes.
func pasteSafe(s string) string {
	if needsANSICQuoting(s) {
		var b strings.Builder
		b.WriteString("$'")
		for _, r := range s {
			switch r {
			case '\'':
				b.WriteString(`\'`)
			case '\\':
				b.WriteString(`\\`)
			case 0x1b:
				b.WriteString(`\e`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 0x20 || r == 0x7f {
					fmt.Fprintf(&b, `\x%02x`, r)
					continue
				}
				b.WriteRune(r)
			}
		}
		b.WriteString("'")
		return b.String()
	}
	return shellQuoteIfNeeded(s)
}

// needsANSICQuoting is true for the values single quotes cannot make safe: a
// control byte inside single quotes is still a control byte on its way to the
// terminal.
func needsANSICQuoting(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// tombstoneHint is the sentence both writers print when the note they just
// wrote lands under a tombstone. One sentence, one id, quoted once: `remember`
// and `promote` each had their own copy of it.
func tombstoneHint(what, id string) string {
	return fmt.Sprintf("deja: %s but stays hidden — %s was forgotten, and its tombstone lifts only through `deja forget --unforget deja:%s`\n",
		what, safeForStatusline(id, 200), pasteSafe(id))
}
