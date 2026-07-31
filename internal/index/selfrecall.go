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

// Harness plumbing is the same problem one step out: text a harness injects
// into its own transcript, which deja then indexes as if a person had said it.
// Measured on a real index (#551): 833 records, 0.82 MB — 1.2% of records and
// 1.9% of the text. `[Request interrupted by user]` is not memory, and the hook
// envelope is deja's own output arriving by a route the #488 filter did not
// cover.
//
// Only wrappers whose *content* is the harness talking are listed. `<bash-stdout>`
// deliberately is not: the tags are noise but what they wrap is real command
// output, which is the most useful thing in a transcript.
var injectedBlocks = [][2]string{
	{"<system-reminder>", "</system-reminder>"},
	{"<local-command-caveat>", "</local-command-caveat>"},
	{"<command-name>", "</command-args>"},
}

// Codex writes its preamble as a user turn rather than under its own role, so
// a role filter does not reach it. Measured on two stores: it is the first
// user message in 28 of 28 sessions here and titles 81 of 82 there (#636).
// Identical in every session and lexically broad — plugin names, tool
// descriptions, an AGENTS.md persona paragraph — so it outranks real turns on
// aggregate match count, which makes it a ranking bug rather than a cosmetic
// one.
//
// These are stripped only at the *start* of a message, unlike injectedBlocks.
// The tags are ordinary enough that a person quotes them: on this store nine
// Claude sessions contain `<environment_context>` and two contain
// `<user_instructions>`, every one of them a real turn discussing the very
// filtering this list implements. A preamble is always the head of the
// message, so requiring that costs nothing and stops the filter from eating
// someone's sentence.
var injectedPrefixBlocks = [][2]string{
	{"<environment_context>", "</environment_context>"},
	{"<recommended_plugins>", "</recommended_plugins>"},
	{"<permissions instructions>", "</permissions instructions>"},
	{"<skills_instructions>", "</skills_instructions>"},
	{"<user_instructions>", "</user_instructions>"},
}

// agentsHeading is the line Codex writes above the AGENTS.md block it injects,
// and it needs its own rule rather than a place in the list above.
//
// As a plain open/close pair it spans from the heading to the *next*
// `</INSTRUCTIONS>` anywhere in the message, and the two do not have to belong
// together. A person writing "# AGENTS.md instructions — how do I stop Codex
// injecting these? Mine is: <INSTRUCTIONS>be terse</INSTRUCTIONS>" lost the
// whole question, which is precisely the message someone writes when reporting
// this bug. Requiring the opening tag to follow the heading immediately makes
// the span the injected block and nothing else.
const (
	agentsHeading = "# AGENTS.md instructions"
	agentsOpen    = "<INSTRUCTIONS>"
	agentsClose   = "</INSTRUCTIONS>"
)

// Whole-line markers: a harness note that occupies its own line, with no
// closing tag. Matched at line granularity so a line quoting one inside a real
// message does not take the message with it.
var injectedLines = []string{
	"[Request interrupted by user",
	"[Your previous response had no visible output",
	"UserPromptSubmit hook additional context:",
	"SessionStart hook additional context:",
}

// stripSelfRecall removes every recall block from a message, keeping whatever
// the user or agent actually wrote around it. Most harnesses prepend the block
// to a real user turn, so dropping the whole message would lose the question
// that prompted the session.
func stripSelfRecall(text string) string {
	for _, m := range selfRecallMarkers {
		text = stripBetween(text, m[0], m[1])
	}
	for _, m := range injectedBlocks {
		text = stripBetweenClosed(text, m[0], m[1])
	}
	text = stripInjectedPrefixes(text)
	return stripInjectedLines(text)
}

// stripInjectedLines drops lines that are entirely a harness marker. The line
// has to *start* with the marker: a message discussing one — this file, an
// issue, a bug report — keeps it.
func stripInjectedLines(text string) string {
	for _, m := range injectedLines {
		if !strings.Contains(text, m) {
			continue
		}
		lines := strings.Split(text, "\n")
		kept := lines[:0]
		for _, ln := range lines {
			if strings.HasPrefix(strings.TrimSpace(ln), m) {
				continue
			}
			kept = append(kept, ln)
		}
		text = strings.Join(kept, "\n")
	}
	return text
}

// stripInjectedPrefixes removes a harness preamble from the head of a message,
// repeatedly: Codex stacks several of them before the first real turn.
func stripInjectedPrefixes(text string) string {
	for again := true; again; {
		again = false
		trimmed := strings.TrimSpace(text)
		if rest, ok := stripAgentsBlock(trimmed); ok {
			text, again = rest, true
			continue
		}
		for _, m := range injectedPrefixBlocks {
			if !strings.HasPrefix(trimmed, m[0]) {
				continue
			}
			end, ok := closerAfter(trimmed, m[0], m[1])
			if !ok {
				continue
			}
			text = trimmed[end:]
			again = true
			break
		}
	}
	return text
}

// stripAgentsBlock removes the AGENTS.md preamble, which is a heading followed
// immediately by the block it introduces. Anything else that begins with the
// same words is someone talking about it.
func stripAgentsBlock(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, agentsHeading) {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[len(agentsHeading):])
	if !strings.HasPrefix(rest, agentsOpen) {
		return "", false
	}
	end, ok := closerAfter(rest, agentsOpen, agentsClose)
	if !ok {
		return "", false
	}
	return rest[end:], true
}

// closerAfter returns the offset just past the closer that matches the opener
// at the head of text, counting nested copies of the same tag. Without the
// count, `<T>a<T>b</T>c</T>tail` strips to `c</T>tail` and leaves a dangling
// closer in the index.
func closerAfter(text, open, close string) (int, bool) {
	depth := 1
	i := len(open)
	for i < len(text) {
		nextClose := strings.Index(text[i:], close)
		if nextClose < 0 {
			return 0, false
		}
		nextOpen := strings.Index(text[i:], open)
		if nextOpen >= 0 && nextOpen < nextClose {
			depth++
			i += nextOpen + len(open)
			continue
		}
		depth--
		i += nextClose + len(close)
		if depth == 0 {
			return i, true
		}
	}
	return 0, false
}

// stripBetweenClosed removes only *complete* blocks. An unclosed marker is
// treated as prose, which is the opposite of the rule for deja's own recall: a
// truncated <deja-recall> is still ours, but a sentence mentioning
// <system-reminder> — a bug report, this file's own tests — is not, and
// swallowing the rest of it loses what the person actually wrote.
func stripBetweenClosed(text, open, close string) string {
	for {
		start := strings.Index(text, open)
		if start < 0 {
			return text
		}
		rel := strings.Index(text[start+len(open):], close)
		if rel < 0 {
			return text
		}
		text = text[:start] + text[start+len(open)+rel+len(close):]
	}
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
