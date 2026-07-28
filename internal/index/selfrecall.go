package index

import "strings"

// deja injects recall into an agent's context wrapped in <deja-recall> markers.
// The harness then writes that context into its own transcript, and deja indexes
// transcripts — so without this, deja's output becomes deja's input. Measured on
// a real machine before the filter existed: 29 sessions across claude, kimi and
// gemini held copies of blocks deja had written itself.
//
// The consequences compound rather than merely waste space. A search can return
// a past recall summary instead of the session it summarised; every session that
// recalls a fact adds another copy of it, inflating that fact's term frequency
// for reasons that have nothing to do with the user; and `reused N× by agents`
// cannot tell a genuine reuse from deja reading its own injection.
//
// The markers are used rather than the header text because the text is
// translated into each harness's own framing while the tags survive verbatim —
// verified across claude, codex, gemini, kimi, qwen and copilot transcripts.
// Harnesses do not agree on how to store the markers. gemini HTML-escapes them
// into its hook_context wrapper, so a filter that only knew the raw form left
// every gemini session polluted while looking like it worked everywhere else.
// Each pair is tried in turn.
var selfRecallMarkers = [][2]string{
	{"<deja-recall>", "</deja-recall>"},
	{"&lt;deja-recall&gt;", "&lt;/deja-recall&gt;"},
}

// stripSelfRecall removes every recall block from a message, keeping whatever
// the user or agent actually wrote around it. Most harnesses prepend the block
// to a real user turn, so dropping the whole message would lose the question
// that prompted the session.
func stripSelfRecall(text string) string {
	for _, m := range selfRecallMarkers {
		text = stripBetween(text, m[0], m[1])
	}
	return text
}

func stripBetween(text, open, close string) string {
	if !strings.Contains(text, open) {
		return text
	}
	var b strings.Builder
	rest := text
	for {
		start := strings.Index(rest, open)
		if start < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:start])
		after := rest[start+len(open):]
		end := strings.Index(after, close)
		if end < 0 {
			// An unclosed marker means the block was truncated — by a context
			// cap, a crash, or a harness that stores a prefix. Everything from
			// the marker on is ours, so none of it should be indexed.
			break
		}
		rest = after[end+len(close):]
	}
	out := b.String()
	// Collapse the blank lines the removal leaves behind, so a message that was
	// text-block-text does not keep a hole where the block used to be.
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	if strings.TrimSpace(out) == "" {
		return ""
	}
	// Removing a block at either end leaves the newline that separated it from
	// the real text; a message that now starts with a blank line is an artefact
	// of the removal, not something anyone wrote.
	return strings.Trim(out, "\n")
}
