package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// A session start on a checkout with no sessions of its own still injects the
// environment block — the "this machine keeps hitting X" list. The event is
// logged empty because no project session went into it, and the log used that
// flag to print "(empty result)" beside the bytes the block actually sent
// (#1954).
func TestTheLogDoesNotCallAnInjectionWithBytesEmpty(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	withHookStdin(t, `{"source":"startup","session_id":"ses_env"}`)
	out := captureStdout(t, func() {
		if err := runHookContext(dir, true); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "separate sessions") {
		t.Fatalf("the environment block did not go out, so there is no injection to report:\n%q", out)
	}

	var b strings.Builder
	if err := runLogTo(&b, dir, nil); err != nil {
		t.Fatal(err)
	}
	line := b.String()
	if !strings.Contains(line, usage.KindHook) {
		t.Fatalf("the log has no injection row:\n%s", line)
	}
	if strings.Contains(line, "(empty result)") {
		t.Errorf("the log calls an injection that sent bytes an empty result:\n%s", line)
	}
}

// The mark still belongs on the event it was written for, and that event is
// not a zero-byte one: a recall that matched nothing serves the sentence saying
// so, so it carries bytes and the flag together. A rule reading the bytes
// instead of the kind would drop the mark exactly where it is the point.
func TestTheLogStillMarksARecallThatMatchedNothing(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	text, err := callMCPTool(dir, "recall", json.RawMessage(`{"query":"nothing here matches this at all"}`))
	if err != nil {
		t.Fatal(err)
	}
	if text == "" {
		t.Fatal("an empty recall answers with the sentence saying so; it answered with nothing")
	}

	var b strings.Builder
	if err := runLogTo(&b, dir, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "(empty result)") {
		t.Errorf("a recall that matched nothing is no longer marked:\n%s", b.String())
	}
}
