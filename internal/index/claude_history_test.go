package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func claudeHistoryEntry(t *testing.T, id, cwd, display string, at time.Time) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"display": display, "pastedContents": map[string]any{}, "timestamp": at.UnixMilli(), "project": cwd, "sessionId": id})
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// A session known only from Claude Code's prompt log is one session however
// many prompts it has, and an append reads the same as a rebuild (#5046).
func TestClaudeHistoryAppendMatchesARebuild(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	h := filepath.Join(tmp, "history.jsonl")
	at := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	parityWrite(t, h, claudeHistoryEntry(t, "gone-1", "/tmp/proj", "why does the retry loop spin", at), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	parityWrite(t, h, claudeHistoryEntry(t, "gone-1", "/tmp/proj", "and why does the backoff never reset", at.Add(time.Minute)), true)
	parityPass(t, inc, false)
	sameAsRebuild(t, inc)
	s, ok, err := FindByIdentity(inc, "claude", "gone-1")
	if err != nil || !ok || len(s.Messages) != 2 || s.Path != h {
		t.Fatalf("prompts-only session = %+v ok=%v err=%v", s, ok, err)
	}
}

// A transcript deja kept after Claude Code deleted it holds the prompt the log
// holds too. The log is read for sessions with no transcript on disk, which a
// kept one is: its prompt was merged in a second time, and on the append path
// the log took the row because the kept path was gone (#5046).
func TestAKeptTranscriptIsNotShadowedByItsPrompts(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	h := filepath.Join(tmp, "history.jsonl")
	tr := filepath.Join(tmp, "claude", "-tmp-proj", "kept-1.jsonl")
	parityWrite(t, tr, parityClaudeLine("kept-1", "00:00Z", "user", `"fix the flaky pool test"`)+
		parityClaudeLine("kept-1", "00:05Z", "assistant", `"the conn was never closed, closing it in the defer"`), false)
	asked := time.Date(2026, 7, 17, 9, 0, 0, 400_000_000, time.UTC)
	parityWrite(t, h, claudeHistoryEntry(t, "kept-1", "/tmp/proj", "fix the flaky pool test", asked), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	if err := os.Remove(tr); err != nil {
		t.Fatal(err)
	}
	parityPass(t, inc, false)
	before := paritySnapshot(t, inc)["claude:kept-1"]
	if before == "" {
		t.Fatal("the deleted transcript was not kept")
	}
	check := func(when string) {
		t.Helper()
		s, ok, err := FindByIdentity(inc, "claude", "kept-1")
		if err != nil || !ok {
			t.Fatalf("%s: kept session gone: %v", when, err)
		}
		if s.Path != tr {
			t.Errorf("%s: the row is %s, want the kept transcript %s", when, s.Path, tr)
		}
		n := 0
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "fix the flaky pool test") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: the prompt is held %d times: %+v", when, n, s.Messages)
		}
	}
	// A line the log gains for the same id reaches the append path, where the
	// kept row's path no longer exists.
	parityWrite(t, h, claudeHistoryEntry(t, "kept-1", "/tmp/proj", "fix the flaky pool test", asked.Add(time.Second))+
		claudeHistoryEntry(t, "gone-2", "/tmp/proj", "what did the pool fix change", asked.Add(time.Hour)), true)
	parityPass(t, inc, false)
	check("after an append")
	// A rebuild reads every line of the log again, the kept one's included.
	parityPass(t, inc, true)
	check("after a rebuild")
	if got := paritySnapshot(t, inc)["claude:kept-1"]; got != before {
		t.Errorf("the kept session changed:\n  %s\nwant\n  %s", got, before)
	}
	if _, ok, _ := FindByIdentity(inc, "claude", "gone-2"); !ok {
		t.Error("a session only the log knows was not read")
	}
}

// Codex's history.jsonl had the same hole: a rollout deja kept after it was
// deleted got its prompt again from the log on the next rebuild.
func TestAKeptCodexRolloutIsNotShadowedByItsPrompt(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	const id = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0009"
	roll := filepath.Join(tmp, "codex", "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-"+id+".jsonl")
	parityWrite(t, roll, `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/tmp/proj"}}`+"\n"+
		`{"timestamp":"2026-07-17T09:00:01.437Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the retry loop spin"}]}}`+"\n"+
		`{"timestamp":"2026-07-17T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the backoff never resets"}]}}`+"\n", false)
	parityWrite(t, filepath.Join(tmp, "codex", "history.jsonl"), `{"session_id":"`+id+`","ts":1784278801,"text":"why does the retry loop spin"}`+"\n", false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	if err := os.Remove(roll); err != nil {
		t.Fatal(err)
	}
	parityPass(t, inc, false)
	parityPass(t, inc, true)
	s, ok, err := FindByIdentity(inc, "codex", id)
	if err != nil || !ok {
		t.Fatalf("kept rollout gone: %v", err)
	}
	n := 0
	for _, m := range s.Messages {
		if m.Text == "why does the retry loop spin" {
			n++
		}
	}
	if n != 1 || s.Path != roll || s.Project == "history" {
		t.Errorf("kept rollout after a rebuild: prompt held %d times, row %s, project %q", n, s.Path, s.Project)
	}
}
