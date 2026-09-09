package digest

import (
	"regexp"
	"strings"
)

// harnessBlockTags are the envelopes a harness wraps around, or delivers as,
// a user turn: what the host says to the model, recorded under the user's
// role. A prompt is often a person's words with one of these appended; a
// prompt that is only one of these is the host talking to itself.
//
// The list is what this machine's own transcripts hold under the user role
// (counted on 2026-09-08): Claude Code's teammate-message ×656,
// task-notification ×653, command-message ×108, bash-input/stdout/stderr
// (the `!` shell), local-command-stdout; Copilot's skill-context; Kimi's
// hook_result; opencode's meta; Codex's environment and instruction blocks.
var harnessBlockTags = []string{
	"task-notification", "system-reminder", "teammate-message",
	"local-command-stdout", "local-command-stderr", "local-command-caveat",
	"command-name", "command-message", "command-args",
	"bash-input", "bash-stdout", "bash-stderr",
	"environment_context", "user_instructions", "permissions instructions",
	"skill-context", "meta", "hook_result", "hook_context", "deja-recall",
	// Amp and Cursor put the attached file bodies ahead of the words: the
	// question under them spent the term budget on `attached_files`, `file`,
	// `path` and the file's opening and never reached the ranking.
	"attached_files", "additional_data",
}

// userQueryTagRe is Cursor's wrapper around the words a person typed; the
// tags go, the words stay.
var userQueryTagRe = regexp.MustCompile(`(?i)</?user_query>`)

// orphanCloseRe is the closing tag a nested block leaves behind: the block
// regexp stops at the first closing tag of any listed name, so Cursor's
// `<additional_data>…<attached_files>…</attached_files></additional_data>`
// keeps its outer close. Only a closing tag standing on a line of its own
// goes — "I removed the </attached_files> line" is a person's sentence.
var orphanCloseRe = func() *regexp.Regexp {
	alts := make([]string, 0, len(harnessBlockTags))
	for _, t := range harnessBlockTags {
		alts = append(alts, regexp.QuoteMeta(t))
	}
	return regexp.MustCompile(`(?im)^[ \t]*</\s*(?:` + strings.Join(alts, "|") + `)\s*>[ \t]*$`)
}()

// hostPreambleBannerRE is the line a host prints above a turn it hands the
// prompt hook to say the turn is not the user — Claude Code's
// "[SYSTEM NOTIFICATION - NOT USER INPUT]" over a finished background job.
// Stripping the block under it was not enough: the paragraph between the banner
// and the block is the host too, and the hook read "no human input has been
// received since the last genuine user message" as a question and answered it
// out of the store, on `received`, `since` and `last`.
var hostPreambleBannerRE = regexp.MustCompile(`(?i)^[ \t]*\[[^\]\n]*not\s+(?:a\s+)?user\s+input[^\]\n]*\][ \t]*$`)

// stripHostPreamble drops the banner and the paragraph under it, ending at the
// first blank line. What follows a blank line is left alone: someone who pastes
// a notification to ask about it keeps their question, the choice #3168 made
// for a hook's own status bar.
func stripHostPreamble(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	inPreamble := false
	for _, l := range lines {
		if hostPreambleBannerRE.MatchString(l) {
			inPreamble = true
			continue
		}
		if inPreamble {
			if strings.TrimSpace(l) == "" {
				inPreamble = false
			}
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

var harnessBlockRe = func() *regexp.Regexp {
	alts := make([]string, 0, len(harnessBlockTags))
	for _, t := range harnessBlockTags {
		alts = append(alts, regexp.QuoteMeta(t))
	}
	// A closed block, or an unclosed one that runs to the end of the text —
	// hosts truncate long notifications and the tail is still not the user.
	return regexp.MustCompile(`(?is)<(` + strings.Join(alts, "|") + `)(?:\s[^>]*)?>(?:.*?</\s*` + "(?:" + strings.Join(alts, "|") + `)\s*>|.*$)`)
}()

// closedHarnessBlockREs is one regexp per tag: a block that opens and closes
// with the same name. harnessBlockRe closes on any listed name, which is what
// a nested Cursor block needs, but on a turn where two different envelopes
// stand apart it swallowed the sentence between them (review of #3323).
var closedHarnessBlockREs = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(harnessBlockTags))
	for _, t := range harnessBlockTags {
		q := regexp.QuoteMeta(t)
		out = append(out, regexp.MustCompile(`(?is)<`+q+`(?:\s[^>]*)?>.*?</\s*`+q+`\s*>`))
	}
	return out
}()

// unwrapUserQuery removes Cursor's wrapper where both of its tags are there,
// keeping the words between them. Each close pairs with the nearest open
// before it: a regexp runs from the first open to the first close, so a turn
// that opens one and never closes it, and closes a later one, kept the stray
// tag inside what it handed back (review of #3323). An open with no close is
// a person naming the tag, and is left where it is.
func unwrapUserQuery(text string) string {
	const open, close = "<user_query>", "</user_query>"
	for {
		end := strings.Index(text, close)
		if end < 0 {
			return text
		}
		start := strings.LastIndex(text[:end], open)
		if start < 0 {
			// A close with nothing open before it: the orphan rule owns that.
			return text
		}
		text = text[:start] + text[start+len(open):end] + text[end+len(close):]
	}
}

// StripClosedHarnessBlocks removes the envelopes a harness opened and closed
// under one name, and leaves everything else as it stands. The prompt hook
// wants the stricter rule — a truncated notification is still not the person —
// but a surface that prints the turn back must not lose words to a tag
// somebody typed themselves.
func StripClosedHarnessBlocks(text string) string {
	if !strings.Contains(text, "<") {
		return strings.TrimSpace(text)
	}
	for _, re := range closedHarnessBlockREs {
		text = re.ReplaceAllString(text, "")
	}
	// What a nested block leaves behind, and Cursor's wrapper around the words
	// a person typed: the tags go, the words stay. The wrapper is unwrapped as
	// a pair — a lone `<user_query>` in a sentence about Cursor is someone
	// naming the tag, and taking it out of their prose mangles the sentence
	// (review of #3323).
	text = orphanCloseRe.ReplaceAllString(text, "")
	text = unwrapUserQuery(text)
	return strings.TrimSpace(text)
}

// StripHarnessBlocks returns what is left of a prompt once the harness's own
// envelopes are removed: the person's words, or "" when the turn was the host
// alone. A task notification delivered as a user turn once made the prompt
// hook say "you have been here" about another notification, with the
// envelope's field names as the identifying terms (#3156) — and once the block
// was gone, about the host's own paragraph above it.
func StripHarnessBlocks(prompt string) string {
	if strings.Contains(prompt, "[") {
		prompt = stripHostPreamble(prompt)
	}
	if strings.Contains(prompt, "<") {
		prompt = harnessBlockRe.ReplaceAllString(prompt, "")
		prompt = orphanCloseRe.ReplaceAllString(prompt, "")
		prompt = userQueryTagRe.ReplaceAllString(prompt, "")
	}
	if strings.Contains(prompt, " says: ") {
		prompt = stripHookStatusLines(prompt)
	}
	return strings.TrimSpace(prompt)
}

// stripHookStatusLines drops the lines that are a hook's status bar pasted
// into the turn — `UserPromptSubmit says: deja-vu — you have been here: …`
// above the question a person went on to ask about it (#3168). The question
// stays; the bar is neither a title nor terms.
func stripHookStatusLines(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if hookStatusLineRE.MatchString(strings.TrimSpace(l)) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}
