package digest

import "testing"

// Claude Code writes what a hook returned into the transcript under the user
// role, so deja's own status line comes back as something the person said —
// and gets quoted as the session's title. Seen in the status bar as
// `you have been here: "UserPromptSubmit says: deja-vu — you have been h…"`,
// which is recall quoting itself (#3168).
func TestHookEchoesAreNotWhatThePersonSaid(t *testing.T) {
	echoes := []string{
		`UserPromptSubmit says: deja-vu — you have been here: "the retry cap" (today)`,
		`SessionStart:compact says: deja recalled 3 prior sessions for this project`,
		`⎿ SessionStart:startup says: deja: recalled 2 sessions`,
		`PreToolUse says: deja: this command failed here before`,
		`PreCompact:manual says: deja saved the session digest`,
		"<deja-recall>\nRecalled history from prior sessions.\n</deja-recall>",
		"the assistant answered, and the transcript kept\n<deja-recall>\nan echoed block\n</deja-recall>",
	}
	for _, e := range echoes {
		if !IsPlumbing(e) {
			t.Errorf("IsPlumbing missed a host echo: %q", e)
		}
		if !IsAgentArtifact(e) {
			t.Errorf("IsAgentArtifact missed a host echo: %q", e)
		}
	}

	// What a person actually wrote, including the shapes this could have eaten:
	// a name in front of "says:", a quotation mid-sentence, and a message that
	// merely talks about the hook.
	mine := []string{
		"Vlad says: rebase first, then push",
		"the reviewer says: this needs a test",
		"he says: no, and I think he is right",
		"why does UserPromptSubmit fire on a task notification",
		"SessionStart is the only event goose gives us at the top of a session",
		"the hook says nothing when the index has no history for the command",
	}
	for _, m := range mine {
		if IsPlumbing(m) {
			t.Errorf("IsPlumbing discarded something the person wrote: %q", m)
		}
		if IsAgentArtifact(m) {
			t.Errorf("IsAgentArtifact discarded something the person wrote: %q", m)
		}
	}
}
