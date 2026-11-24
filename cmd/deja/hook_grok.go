package main

import (
	"os"

	"github.com/vshulcz/deja-vu/internal/hookengine"
)

// grokEnvelope embeds hookengine's Grok camelCase fields so legacy callers on
// unexported hook input structs adopt them without duplicating field aliasing.
type grokEnvelope struct {
	hookengine.GrokEnvelope
}

func (e grokEnvelope) adoptCommon(sessionID, conversationID, transcriptPath string, roots []string) (string, string, []string) {
	return e.AdoptCommon(sessionID, conversationID, transcriptPath, roots)
}

func adoptGrok(common, grok string) string {
	return hookengine.AdoptValue(common, grok)
}

func adoptGrokRoots(common []string, grok string) []string {
	return hookengine.AdoptRoots(common, grok)
}

// grokDropsContext reports whether this hook runs under Grok Build for an event
// whose context grok throws away. Its hook guide says SessionStart's stdout is
// ignored and an allowing UserPromptSubmit's additionalContext is discarded,
// and a 1.0.41 session's chat_history.jsonl — what the model was sent — held
// no deja-recall from either, while the receipt said "1.7 KB of context" and
// the log counted memory arriving (#4588). Only PreToolUse and PostToolUse
// context reaches the model there.
//
// GROK_HOOK_EVENT is set by grok on every command hook it runs, the Claude
// Code hooks it also reads included, and a user's own value for it is
// overridden. The payload is not read for this: one replayed by hand, or by a
// test, is a question about what deja would recall, and gets the answer.
func grokDropsContext() bool {
	switch os.Getenv("GROK_HOOK_EVENT") {
	case "session_start", "user_prompt_submit":
		return true
	}
	return false
}
