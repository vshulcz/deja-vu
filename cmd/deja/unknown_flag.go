package main

import (
	"fmt"
	"strings"
)

// The 23 places deja refuses a flag it does not take used to be 23 separate
// sentences, and exactly one of them helped: search names the flag you
// probably meant, because folding a near-miss into the query silently turned a
// working search into "you have no such memory" (#755). Someone who types
// `--limt` at `deja show` has made the same mistake and got less help, and
// search was also the only one that left its own name out of the refusal, so a
// reader could not tell which parser had spoken (#1829).
//
// One sentence, then, in one place: `<command>: unknown flag "x"`, plus the
// near miss when there is one, or where the flags are listed when there is not.

// nearestKnownFlag names the flag a token was probably meant to be, or "" when
// nothing is close enough to be worth guessing at.
//
// Conservative on purpose, and deliberately the rule search has been using:
// the token has to look like a long flag, because an argument that starts with
// a single dash may be a value and a bare word is not a flag at all, and it
// has to be within a prefix or two edits of a real one — a dropped letter, a
// doubled one, a transposition.
func nearestKnownFlag(a string, known []string) string {
	if !strings.HasPrefix(a, "--") || len([]rune(a)) < 4 {
		return ""
	}
	for _, f := range known {
		if a == f {
			return ""
		}
	}
	return nearestTarget(a, known)
}

// unknownFlag is how a command refuses a flag it does not take: named by the
// command that refused, so a reader of two refusals side by side knows which
// parser spoke, and carrying the near miss when there is one.
func unknownFlag(command, arg string, known []string) error {
	if near := nearestKnownFlag(arg, known); near != "" {
		return fmt.Errorf("%s: unknown flag %q — did you mean %s?", command, arg, near)
	}
	// Without a near miss the reader still needs the list, and the command's
	// own --help is it.
	return fmt.Errorf("%s: unknown flag %q — `deja %s --help` lists its flags", command, arg, strings.Fields(command)[0])
}
