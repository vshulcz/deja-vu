package main

import "os"

// Grok Build sends its hook payload in camelCase throughout, where every other
// harness deja wires sends snake_case — sessionId, workspaceRoot,
// transcriptPath, toolName, toolInput, measured on 1.0.5. Only cwd and prompt
// happen to be spelled the same, which is why grok looked wired: the recall it
// produced was for the right project and the right question, under no session
// at all.
//
// A session with no name of its own shares the empty key in the dedup ledger
// with every other session on the machine, so the second grok session looks
// like it has already been shown everything the first one was, and a compaction
// forgets nothing because there is nothing filed under its name.
//
// The structs below embed this one, so a payload decodes into both spellings at
// once, and each adopts grok's where the common spelling arrived empty.
type grokEnvelope struct {
	SessionID           string `json:"sessionId"`
	ConversationIDCamel string `json:"conversationId"`
	WorkspaceRoot       string `json:"workspaceRoot"`
	TranscriptPath      string `json:"transcriptPath"`
	ToolName            string `json:"toolName"`
	ToolInput           struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	} `json:"toolInput"`
}

func (e grokEnvelope) adoptCommon(sessionID, conversationID, transcriptPath string, roots []string) (string, string, []string) {
	sid := adoptGrok(adoptGrok(adoptGrok(sessionID, e.SessionID), conversationID), e.ConversationIDCamel)
	tp := adoptGrok(transcriptPath, e.TranscriptPath)
	ws := adoptGrokRoots(roots, e.WorkspaceRoot)
	return sid, tp, ws
}

// adoptGrok returns the value every other harness sends, or grok's when that
// one is absent.
func adoptGrok(common, grok string) string {
	if common != "" {
		return common
	}
	return grok
}

// adoptGrokRoots is the same for the project path, which grok names once where
// cursor sends a list.
func adoptGrokRoots(common []string, grok string) []string {
	if len(common) > 0 || grok == "" {
		return common
	}
	return []string{grok}
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
