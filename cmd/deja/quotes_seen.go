package main

import (
	"strings"
)

// A quote is not news because it arrived under a different session id.
//
// The block is deduped whole, and its header carries the session's id and date —
// so the same sentence from a fork, a subagent transcript or a resumed session
// passes as something new. Measured on a real store with eight near-identical
// nudges, the shape a reader produces when they keep saying "carry on": nine
// distinct sessions cited, fifteen quoted lines of which eleven were distinct,
// and the worst line arrived four times under four ids. Seen live in the
// session that wrote this.
const quoteSeenPrefix = "q:"

// quoteKey identifies what a quoted line says, ignoring who said it and where
// it came from.
func quoteKey(text string) string {
	return quoteSeenPrefix + shortHash(strings.Join(strings.Fields(text), " "))
}

// isRecallQuote reports whether the line is one of the quoted excerpts, and
// returns what it quotes.
func isRecallQuote(line string) (string, bool) {
	t := strings.TrimSpace(line)
	for _, p := range []string{"- User: ", "- Assistant: "} {
		if rest, ok := strings.CutPrefix(t, p); ok {
			return rest, true
		}
	}
	return "", false
}

// isRecallHeader reports whether the line opens a session's entry.
func isRecallHeader(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "✓ recalled from ") ||
		(strings.HasPrefix(t, "- **") && strings.Contains(t, "` "))
}

// withoutSeenQuotes drops the quoted lines this agent session has already been
// shown, and any session entry left with nothing to say.
//
// The last return says whether the block still has something to deliver. It is
// false only when the block carried quotes and every one of them had been
// delivered already — a block that never quoted anything, the weak pointer
// among them, is unchanged and still worth its line.
func withoutSeenQuotes(seen map[string]bool, body string) (string, []string, bool) {
	lines := strings.Split(body, "\n")
	var out []string
	var kept []string
	// A session entry is a header and the lines under it, which is what has to
	// be dropped together — a header alone reads as a session deja found and
	// then said nothing about.
	var group []string
	groupQuotes := 0
	hadQuotes := false
	inBlock := map[string]bool{}
	flush := func() {
		if len(group) == 0 {
			return
		}
		if groupQuotes > 0 {
			out = append(out, group...)
		}
		group = nil
		groupQuotes = 0
	}
	for _, line := range lines {
		if isRecallHeader(line) {
			flush()
			group = []string{line}
			continue
		}
		if text, ok := isRecallQuote(line); ok {
			hadQuotes = true
			key := quoteKey(text)
			// Already delivered, or already in this block: two sessions in one
			// answer quote the same sentence as readily as two answers do —
			// measured, that was half of what a run of nudges repeated.
			if seen[key] || inBlock[key] {
				continue
			}
			inBlock[key] = true
			kept = append(kept, key)
			if group != nil {
				group = append(group, line)
				groupQuotes++
			} else {
				out = append(out, line)
			}
			continue
		}
		// The metadata under a header belongs to its group; everything else is
		// the block's own framing.
		if group != nil && strings.HasPrefix(line, "  - ") {
			group = append(group, line)
			continue
		}
		flush()
		out = append(out, line)
	}
	flush()
	if !hadQuotes {
		return body, nil, true
	}
	return strings.Join(out, "\n"), kept, len(kept) > 0
}
