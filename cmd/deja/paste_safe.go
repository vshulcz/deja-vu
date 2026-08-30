package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pasteSafe renders a value the reader is meant to paste back into a shell.
//
// The tombstone hint is the case that forced it: the id is the argument of the
// command that lifts the tombstone, so sanitising it hands over a command that
// names nothing — and printing it raw sends an escape byte from a project name
// straight to the terminal (#1794, the half #1792 left).
//
// Two forms, and the choice is not about looks. A value carrying anything a
// terminal acts on goes out ANSI-C quoted — the bytes are visible as `\e`,
// `\t`, `\u200e`, and bash and zsh turn them back into the same bytes. Anything
// else that a shell would act on — `;`, `&`, `>`, a space — goes out single
// quoted. Only a value that is plain by every measure is printed bare.
//
// ANSI-C quoting is bash and zsh; dash and fish have no such form, which is
// why the hint says which shells it is for rather than pretending otherwise.
func pasteSafe(s string) string {
	if !needsANSICQuoting(s) {
		return shellQuoteForPaste(s)
	}
	var b strings.Builder
	b.WriteString("$'")
	for i := 0; i < len(s); {
		// Bytes, not runes: `for range` turns invalid UTF-8 into U+FFFD, so a
		// stray 0xff came back out as three different bytes and the pasted
		// command named a key that does not exist.
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&b, `\x%02x`, s[i])
			i++
			continue
		}
		i += size
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
			switch {
			case r < 0x20 || r == 0x7f:
				fmt.Fprintf(&b, `\x%02x`, r)
			case actsOnATerminal(r):
				// C1 controls — U+009B is CSI on most terminals — and the
				// bidi and format characters, which the prose half strips and
				// this half used to carry through raw.
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteString("'")
	return b.String()
}

// needsANSICQuoting is true for the values single quotes cannot make safe: a
// control byte inside single quotes is still a control byte on its way to the
// terminal, and so is a byte that is not valid UTF-8.
func needsANSICQuoting(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return true
		}
		if actsOnATerminal(r) {
			return true
		}
		i += size
	}
	return false
}

// actsOnATerminal is the predicate safeForStatusline strips by, kept the same
// on purpose: the prose half of a line and the command half of it have to
// agree about which characters a terminal acts on, or one of them leaks what
// the other removed.
func actsOnATerminal(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// shellQuoteForPaste single-quotes anything a shell would act on. The set kept
// bare is the conservative one — letters, digits, and the punctuation that has
// no meaning to a shell in an unquoted word — because the alternative is
// handing the reader a command that runs something else: a project name of
// `proj&&id` produced `deja forget --unforget deja:…-proj&&id`, and pasting it
// ran `id`. The name is not always the reader's own; it comes from a directory
// name or from an MCP tool call.
//
// `=` is not in the set: zsh's EQUALS option is on by default, so a value that
// starts a word with it — `=ls` — is replaced by the path of that command.
func shellQuoteForPaste(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if r >= utf8.RuneSelf {
			// Non-ASCII is not a shell metacharacter, and quoting every
			// accented project name would make the hint look like escaping
			// matters when it does not.
			continue
		}
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_@%+:,./-", r) {
			return shellQuote(s)
		}
	}
	return s
}

// pasteSafeCaveat is the shell the quoted form needs, or empty when any shell
// takes it. Only bash and zsh read `$'…'`; dash and fish pass the `$` and the
// backslashes through literally, so the command matches nothing and says so
// with a straight face. Every line that hands over a quoted value carries the
// caveat, not just the first one that needed it.
func pasteSafeCaveat(quoted string) string {
	if strings.HasPrefix(quoted, "$'") {
		return " (in bash or zsh)"
	}
	return ""
}

// tombstoneHint is the sentence both writers print when the note they just
// wrote lands under a tombstone. One sentence, one id, quoted once: `remember`
// and `promote` each had their own copy of it.
func tombstoneHint(what, id string) string {
	quoted := pasteSafe(id)
	return fmt.Sprintf("deja: %s but stays hidden — %s was forgotten, and its tombstone lifts only through `deja forget --unforget deja:%s`%s\n",
		what, safeForStatusline(id, 200), quoted, pasteSafeCaveat(quoted))
}
