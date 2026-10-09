package termwidth

import "strings"

// Wrap breaks one paragraph into lines no wider than width columns, at spaces
// only. A word wider than the line keeps its own line whole: a path or an
// identifier broken by deja is harder to copy than one the terminal folds.
// Width zero or less is a reader with no screen, and gets the text unchanged
// as a single line. Runs of spaces collapse, which is what a reader of a
// wrapped paragraph expects anyway.
func Wrap(s string, width int) []string {
	if width <= 0 || Columns(s) <= width {
		return []string{s}
	}
	return wrap(s, width, width)
}

// Indent wraps s to width with first before the first line and rest before
// every following one, both counted against the width. Width zero returns
// first+s untouched, the shape piped output has always had.
func Indent(s string, width int, first, rest string) string {
	if width <= 0 || Columns(first+s) <= width {
		return first + s
	}
	lines := wrap(s, floor(width-Columns(first)), floor(width-Columns(rest)))
	for i := range lines {
		if i == 0 {
			lines[i] = first + lines[i]
		} else {
			lines[i] = rest + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// WrapText wraps every line of a multi-line text to width, each under its own
// leading spaces, and leaves fenced code (``` blocks) alone: a code line broken
// at a space is a different line when it is copied back. Width zero returns
// the text unchanged.
func WrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	fenced := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") {
			fenced = !fenced
			continue
		}
		if fenced || Columns(line) <= width {
			continue
		}
		lead := line[:len(line)-len(trimmed)]
		if strings.Contains(lead, "\t") {
			continue
		}
		rest := lead
		// A list item's continuation sits under its text, not under the dash.
		for _, bullet := range []string{"- ", "* ", "· ", "→ "} {
			if strings.HasPrefix(trimmed, bullet) {
				rest = lead + strings.Repeat(" ", Columns(bullet))
				lead += bullet
				trimmed = trimmed[len(bullet):]
				break
			}
		}
		lines[i] = Indent(trimmed, width, lead, rest)
	}
	return strings.Join(lines, "\n")
}

// floor keeps a line usable on a terminal narrower than the indent: below
// twenty columns a wrap reads worse than the terminal's own fold.
func floor(n int) int {
	if n < 20 {
		return 20
	}
	return n
}

func wrap(s string, first, rest int) []string {
	var out []string
	cur, curW, limit := "", 0, first
	for _, word := range strings.Fields(s) {
		w := Columns(word)
		switch {
		case cur == "":
			cur, curW = word, w
		case curW+1+w <= limit:
			cur += " " + word
			curW += 1 + w
		default:
			out = append(out, cur)
			cur, curW, limit = word, w, rest
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}
