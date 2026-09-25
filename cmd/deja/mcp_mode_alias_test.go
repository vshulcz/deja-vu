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
	for _, name := range []string{"recall", "recall_context", "blame", "fix", "how", "orient", "remember"} {
		if _, ok := dispatcherModes[name]; !ok {
			t.Errorf("deja names %q in its own guidance and the dispatcher does not accept it", name)
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
	for mode := range dispatcherModes {
		if _, ok := qField[dispatcherModes[mode]]; !ok {
			t.Errorf("mode %q dispatches to %q, which has no field for q", mode, dispatcherModes[mode])
		}
	}
	for _, mode := range []string{"search", "digest", "recall_context", "context", "recall"} {
		got := string(spreadQ(mode, json.RawMessage(`{"mode":"`+mode+`","q":"carried"}`)))
		if !strings.Contains(got, `"query":"carried"`) {
			t.Errorf("spreadQ(%q) did not carry q into query: %s", mode, got)
		}
	}
}

// And the leads really do say it, so the test above is about this repo's own
// text rather than about a name someone might use.
func TestTheLeadsNameTheDeepRead(t *testing.T) {
	for name, text := range map[string]string{
		"sessionStartLead": sessionStartLead,
		"wideRecallLead":   wideRecallLead,
		"antigravityLead":  antigravityLead,
	} {
		if !strings.Contains(text, "recall_context") {
			t.Errorf("%s no longer names the deep read; the alias it justifies may be dead: %q", name, text)
		}
	}
}
