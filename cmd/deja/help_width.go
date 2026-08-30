package main

import (
	"strings"
)

// wrapUsage lays the help page out for the terminal reading it.
//
// The page is written at its natural width — a usage line names every flag its
// command takes, and that is the point of it — but nothing consulted the
// screen, so twelve of eighty-four lines wrapped on a default 80-column
// terminal and the wrap fell wherever the width happened to land, inside a
// bracketed group as often as not (#1661).
//
// A width of zero is a reader with no screen — a pipe, a file — and gets the
// text exactly as written, the rule printableWidth already sets.
func wrapUsage(text string, width int) string {
	if width <= 0 {
		return text
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, wrapUsageLine(line, width)...)
	}
	return strings.Join(out, "\n")
}

// wrapUsageLine breaks one line at the spaces between its groups, never inside
// one: `[--before 2026-01-01]` is a single thing to read, and a wrap through
// the middle of it costs the reader the flag and its example both.
func wrapUsageLine(line string, width int) []string {
	if len([]rune(line)) <= width {
		return []string{line}
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
	fields := usageGroups(strings.TrimLeft(line, " "))
	// The continuation sits under the command rather than under the margin, so
	// a wrapped line still reads as one usage line.
	hanging := indent + "    "
	var out []string
	cur := indent
	for _, f := range fields {
		next := cur
		if len(strings.TrimSpace(next)) > 0 {
			next += " "
		}
		next += f
		if len([]rune(next)) <= width {
			cur = next
			continue
		}
		if len(strings.TrimSpace(cur)) > 0 {
			out = append(out, cur)
			cur = hanging
		}
		if len([]rune(cur+f)) <= width {
			cur += f
			continue
		}
		// A group wider than the terminal on its own — the parenthesised
		// sentence after a hook command. Keeping it whole would put the line
		// back over the width this exists to respect, so it breaks at its own
		// spaces, which is where a sentence can be broken.
		for _, word := range strings.Fields(f) {
			try := cur
			if strings.TrimSpace(try) != "" {
				try += " "
			}
			try += word
			if len([]rune(try)) > width && strings.TrimSpace(cur) != "" {
				out = append(out, cur)
				cur = hanging + word
				continue
			}
			cur = try
		}
	}
	if strings.TrimSpace(cur) != "" {
		out = append(out, cur)
	}
	return out
}

// usageGroups splits a usage line into the pieces a wrap may fall between: a
// word, or a bracketed or parenthesised group with everything inside it.
func usageGroups(s string) []string {
	var out []string
	depth := 0
	start := -1
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '[' || c == '(':
			if depth == 0 && start < 0 {
				start = i
			}
			depth++
		case c == ']' || c == ')':
			if depth > 0 {
				depth--
			}
		case c == ' ' && depth == 0:
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}
