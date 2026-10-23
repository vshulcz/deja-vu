package sources

import (
	"regexp"
	"strings"
)

// pastedOpenRe is the tag Claude Code opens around text pasted into a prompt:
// `<pasted_content id="dd73">`. The closing tag repeats the id —
// `</pasted_content id="dd73">` — which is what pairs them.
var pastedOpenRe = regexp.MustCompile(`<pasted_content id="([^"<>\n]*)">`)

// UnwrapPastedContent takes Claude Code's paste wrapper off a prompt and keeps
// what was pasted. The tags are the host's markup, not the person's words, and
// left in place they titled sessions `<pasted_content id="dd73"> Set up and…`,
// gave the install's demo question the topic "pasted_content set" and put
// `pasted_content` and the id into the index as terms.
//
// Only an open and a close carrying the same id count: a person who writes
// about the tag, or pastes a text that quotes it, keeps their sentence.
// Matching on the id also unwraps a paste that itself quotes a wrapper from
// the inside out, since each open is paired with its own close.
func UnwrapPastedContent(text string) string {
	if !strings.Contains(text, "<pasted_content id=") || !strings.Contains(text, "</pasted_content id=") {
		return text
	}
	changed := false
	for from := 0; from < len(text); {
		loc := pastedOpenRe.FindStringSubmatchIndex(text[from:])
		if loc == nil {
			break
		}
		openStart, openEnd := from+loc[0], from+loc[1]
		closeTag := `</pasted_content id="` + text[from+loc[2]:from+loc[3]] + `">`
		rel := strings.Index(text[openEnd:], closeTag)
		if rel < 0 {
			from = openEnd
			continue
		}
		closeStart := openEnd + rel
		// A newline where each tag stood: a paste closed mid-line,
		// `session?</pasted_content id="cc08">Can you`, must not glue the last
		// pasted word to the first typed one.
		text = text[:openStart] + "\n" + text[openEnd:closeStart] + "\n" + text[closeStart+len(closeTag):]
		changed = true
		from = openStart
	}
	if !changed {
		return text
	}
	return strings.TrimSpace(text)
}
