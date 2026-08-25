package main

import (
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

// The mark still belongs on the event it was written for: a recall that found
// nothing serves nothing, and saying so is the whole point of the flag.
func TestTheLogStillMarksAResultThatServedNothing(t *testing.T) {
	dir := t.TempDir()
	usage.RecordResultRaw(dir, usage.KindSearch, 0, 0, true, 0)

	var b strings.Builder
	if err := runLogTo(&b, dir, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "(empty result)") {
		t.Errorf("a search that served nothing is no longer marked:\n%s", b.String())
	}
}
