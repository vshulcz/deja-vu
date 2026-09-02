package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// injectedBytes is the size of what the hook actually put in front of the
// model. In plain mode that is the framed block on stdout; in the JSON shape it
// is the additionalContext the reply carries.
func injectedBytes(t *testing.T, hookOutput string) int {
	t.Helper()
	text := strings.TrimSpace(hookOutput)
	if strings.HasPrefix(text, "{") {
		var reply struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(text), &reply); err != nil {
			t.Fatalf("hook reply is not the shape the statusline counts: %v\n%s", err, hookOutput)
		}
		text = reply.HookSpecificOutput.AdditionalContext
	}
	if text == "" {
		t.Fatal("the hook produced nothing, so there is nothing to count")
	}
	return len(text)
}

// A machine whose project has no history of its own still gets the environment
// block at every session start, and on such a day that block is the whole of
// deja's work. The statusline said "0 B injected" through it — the untrue line
// its own comment cites #1403 for — because `Empty` means no session went in,
// not that nothing was served (#1962).
func TestTheStatuslineCountsTheBlockItInjected(t *testing.T) {
	dir := seedWalls(t, index.FrictionMinSessions)
	withHookStdin(t, `{"source":"startup","session_id":"ses_env"}`)
	out := captureStdout(t, func() {
		if err := runHookContext(dir, true); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "separate sessions") {
		t.Fatalf("no environment block went out, so there is nothing to count:\n%q", out)
	}

	var line strings.Builder
	if err := runStatusline(dir, strings.NewReader(""), &line); err != nil {
		t.Fatal(err)
	}
	// Asserted positively: runStatusline returns early on a warmup, a policy
	// rule or a quiet week, and every one of those lines would satisfy "does
	// not say 0 B injected" while saying nothing about the block at all.
	// The size is measured from what went out, not written down here: a byte
	// count in the source pins the block's wording, so changing a sentence in
	// it failed this test for a reason that has nothing to do with counting.
	got := strings.TrimSpace(line.String())
	want := fmt.Sprintf("deja · no agent recalls today · %d B injected", injectedBytes(t, out))
	if got != want {
		t.Errorf("statusline = %q,\n                 want %q", got, want)
	}
}

// The week that contains today has to contain today's bytes. `injected` there
// counts hook deliveries deja pushed unprompted, which is what this was.
func TestTheWeekCountsAnInjectionWithNoProjectSession(t *testing.T) {
	dir := t.TempDir()
	usage.RecordResultRaw(dir, usage.KindHook, 501, 0, true, 0)

	_, _, injected, injectedBytes := usage.Week(dir)
	if injected != 1 || injectedBytes != 501 {
		t.Errorf("week counted %d injections and %d bytes, want 1 and 501", injected, injectedBytes)
	}
	_, _, today := usage.TodayDemand(dir)
	if today != 501 {
		t.Errorf("today counted %d injected bytes, want 501", today)
	}
}

// And a lookup that found nothing stays out of both, which is the flag's other
// half: an empty recall serves the sentence saying so, and counting its bytes
// as memory re-used would be the mirror of the bug above.
func TestARecallThatMatchedNothingStaysOutOfTheDemandCounts(t *testing.T) {
	dir := t.TempDir()
	usage.RecordResultRaw(dir, usage.KindRecall, 211, 0, true, 0)

	if recalls, bytes, _, _ := usage.Week(dir); recalls != 0 || bytes != 0 {
		t.Errorf("week counted an empty recall: %d recalls, %d bytes", recalls, bytes)
	}
	if recalls, bytes, _ := usage.TodayDemand(dir); recalls != 0 || bytes != 0 {
		t.Errorf("today counted an empty recall: %d recalls, %d bytes", recalls, bytes)
	}
}
