package main

import (
	"strings"
	"testing"
)

// Every per-prompt integration has to send the agent session id. The hook skips
// what it already showed a session, keyed by that id, and writes down what it
// shows — with no id both halves are off and the same block goes out again on
// the next message. Measured in deja's own injection log on a real store: of
// 165 blocks, 83 were a word-for-word repeat of an earlier one, and all but
// five of those came within a minute.
func TestPerPromptPayloadsCarryTheSessionID(t *testing.T) {
	for _, tc := range []struct {
		name, src, from string
	}{
		// pi puts no id on the event; it keeps one on the session manager, which
		// every handler reaches through ctx.
		{"pi", piExtensionTS("/bin/deja"), "m.getSessionId ? m.getSessionId() : m.sessionId"},
		{"omp", ompExtensionJS("/bin/deja"), "m.getSessionId ? m.getSessionId() : m.sessionId"},
		{"openclaw", openclawPluginJS("/bin/deja"), "event?.sessionId"},
	} {
		if !strings.Contains(tc.src, "hook-prompt") {
			t.Fatalf("%s: no per-prompt recall in the generated plugin", tc.name)
		}
		// The field has to be in the payload, not merely mentioned: reading a
		// session id and then not sending it is the same defect.
		if !strings.Contains(tc.src, "session_id: sessionID") {
			t.Errorf("%s: payload has no session_id, so recall repeats itself:\n%s",
				tc.name, tc.src)
		}
		// And it has to come from the harness rather than be an empty string
		// threaded through to look right.
		if !strings.Contains(tc.src, tc.from) {
			t.Errorf("%s: the session id is not read from the harness (%s):\n%s",
				tc.name, tc.from, tc.src)
		}
	}
}
