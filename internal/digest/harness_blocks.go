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

// StripHarnessBlocks returns what is left of a prompt once the harness's own
// envelopes are removed: the person's words, or "" when the turn was the host
// alone. A task notification delivered as a user turn once made the prompt
// hook say "you have been here" about another notification, with the
// envelope's field names as the identifying terms (#3156).
func StripHarnessBlocks(prompt string) string {
	if !strings.Contains(prompt, "<") {
		return strings.TrimSpace(prompt)
	}
	return strings.TrimSpace(harnessBlockRe.ReplaceAllString(prompt, ""))
}
