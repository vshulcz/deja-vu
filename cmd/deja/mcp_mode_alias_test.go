package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// deja tells agents to call the deep read by name — "call recall_context with
// the session id printed beside it" — in the session-start lead, the
// wide-recall lead, the antigravity lead, the compaction lead, the trimmed
// digest note and the blame overflow note. On a client that gets the one-tool
// shape, that name is a mode, and the dispatcher rejected it: an agent
// following deja's own instruction spent a call on
// `mode "recall_context" is not one of recall, context, blame, ...`.
func TestEveryToolNameDejaTellsAgentsToCallIsAMode(t *testing.T) {
	for _, name := range []string{"recall", "recall_context", "blame", "fix", "how", "orient", "remember", "handoff"} {
		if _, ok := dispatcherModes[name]; !ok {
			t.Errorf("deja names %q in its own guidance and the dispatcher does not accept it", name)
		}
	}
	for _, mode := range declaredModes() {
		if _, ok := dispatcherModes[mode]; !ok {
			t.Errorf("declaredModes() advertises %q, which dispatcherModes does not accept", mode)
		}
	}
	// Named, not declared: the enum is what a model picks from, and listing
	// two names for one call costs a choice it does not need to make.
	declared := strings.Join(declaredModes(), ",")
	if strings.Contains(declared, "recall_context") {
		t.Errorf("the alias is advertised as well as accepted: %s", declared)
	}
}

// Accepting a mode and then not knowing where to put its subject is worse than
// rejecting it: `search` and `digest` were both in the dispatcher and both
// answered "query required", because the field to copy q into was keyed by
// mode and only the canonical modes had a row.
func TestEveryAcceptedModeKnowsWhereQGoes(t *testing.T) {
	targets := map[string]bool{}
	for mode, target := range dispatcherModes {
		targets[target] = true
		field, ok := qField[target]
		if !ok {
			t.Errorf("mode %q dispatches to %q, which has no field for q", mode, target)
			continue
		}
		if field != "" {
			got := string(spreadQ(mode, json.RawMessage(`{"mode":"`+mode+`","q":"carried"}`)))
			if !strings.Contains(got, `"`+field+`":"carried"`) {
				t.Errorf("spreadQ(%q) did not carry q into %q: %s", mode, field, got)
			}
		}
	}
	for target := range qField {
		if !targets[target] {
			t.Errorf("qField has an entry for %q, which no mode in dispatcherModes dispatches to", target)
		}
	}
}

// And the leads really do say it, so the test above is about this repo's own
// text rather than about a name someone might use.
func TestTheLeadsNameTheDeepRead(t *testing.T) {
	for name, text := range map[string]string{
		"sessionStartLead": sessionStartLead,
		"promptHookLead":   promptHookLead,
		"wideRecallLead":   wideRecallLead,
		"antigravityLead":  antigravityLead,
	} {
		if !strings.Contains(text, "recall_context") {
			t.Errorf("%s no longer names the deep read; the alias it justifies may be dead: %q", name, text)
		}
	}
	for name, text := range map[string]string{
		"sessionStartLead":  sessionStartLead,
		"promptHookLead":    promptHookLead,
		"copilotChatPrompt": copilotChatPrompt("/bin/deja"),
	} {
		if !strings.Contains(text, "context") {
			t.Errorf("%s does not name the 1-tool context mode: %q", name, text)
		}
	}
	if ptr := weakRecallPointer(nil, nil); ptr != "" {
		t.Errorf("empty pointer: %q", ptr)
	}
	if note := contextOthersNote(3, false); !strings.Contains(note, `"recall" mode`) {
		t.Errorf("contextOthersNote does not name the 1-tool recall mode: %q", note)
	}
}
