package main

import (
	"bytes"
	"encoding/json"
)

// Cursor reads additional_context from these hooks and shows a person nothing
// else: user_message appears only when a hook blocks the prompt or the tool
// (cursor-agent 2026.09.02, 4347.index.js beforeSubmitPrompt `continue===false`;
// cursor.com/docs/agent/hooks), and systemMessage is not a field it knows. A
// note handed to it is marked shown and read by nobody, and the once-only ones
// are then gone for Claude Code on the same index.
//
// Every Cursor payload carries cursor_version — the CLI and the editor, and the
// Claude Code hooks Cursor runs from ~/.claude/settings.json too — so the
// payload says which host is asking, whatever the config line was.
var cursorHookHost bool

func adoptCursorHost(payload []byte) {
	var p struct {
		CursorVersion string `json:"cursor_version"`
	}
	_ = json.NewDecoder(bytes.NewReader(payload)).Decode(&p)
	// Set either way: the payload decides, not what an earlier one said.
	cursorHookHost = p.CursorVersion != ""
}

// notesHaveReader reports whether the systemMessage of this reply reaches a
// person. When it does not, the notes stay unspent for a host where it does.
func notesHaveReader() bool {
	return !strictHookOutput && !copilotHookOutput && !cursorHookHost
}
