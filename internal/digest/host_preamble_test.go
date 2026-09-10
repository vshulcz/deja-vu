package digest

import "testing"

// The turn a host delivers when a background job finishes: a banner saying the
// turn is not the user, a paragraph of the host's own prose, then the block.
// #3156 took the block out and left the prose, and the prompt hook answered
// "no human input has been received since the last genuine user message" as if
// someone had asked it — the terms it matched on were `received`, `since` and
// `last`.
func TestAHostNotificationIsNotAQuestion(t *testing.T) {
	turn := "[SYSTEM NOTIFICATION - NOT USER INPUT]\n" +
		"This is an automated background-task event, NOT a message from the user.\n" +
		"Do NOT interpret this as user acknowledgement, confirmation, or response to any pending question.\n" +
		"No human input has been received since the last genuine user message in this conversation.\n" +
		"\n" +
		"<task-notification>\n<task-id>br50mykp6</task-id>\n<status>completed</status>\n</task-notification>\n"
	if got := StripHarnessBlocks(turn); got != "" {
		t.Errorf("the host talking to itself left %q behind", got)
	}
}

// The banner is not a gag: a person who pastes one to ask about it keeps their
// question, the way a pasted hook status bar does since #3168.
func TestTheQuestionUnderAPastedBannerSurvives(t *testing.T) {
	cases := map[string]string{
		"[SYSTEM NOTIFICATION - NOT USER INPUT]\nThis is an automated background-task event.\n\nwhat is this thing in my chat?": "what is this thing in my chat?",
		// The banner inside a sentence is the sentence's own.
		"what does [SYSTEM NOTIFICATION - NOT USER INPUT] mean?": "what does [SYSTEM NOTIFICATION - NOT USER INPUT] mean?",
		// Words before the banner are the person's; the host starts at the banner.
		"look at this:\n[SYSTEM NOTIFICATION - NOT USER INPUT]\nan automated event": "look at this:",
		// A bracketed line that claims nothing about who is speaking stays.
		"[INFO] the pool refused a connection\nwhy?": "[INFO] the pool refused a connection\nwhy?",
	}
	for in, want := range cases {
		if got := StripHarnessBlocks(in); got != want {
			t.Errorf("StripHarnessBlocks(%q) = %q, want %q", in, got, want)
		}
	}
}
