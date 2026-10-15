package index

import (
	"encoding/json"
	"fmt"
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

// countPrompt is how many messages of a session hold text, and the row's path.
func countPrompt(t *testing.T, dir, harness, id, text string) (int, string) {
	t.Helper()
	s, ok, err := FindByIdentity(dir, harness, id)
	if err != nil || !ok {
		t.Fatalf("%s:%s not indexed: %v", harness, id, err)
	}
	n := 0
	for _, m := range s.Messages {
		if strings.Contains(m.Text, text) {
			n++
		}
	}
	return n, s.Path
}

// Claude Code writes the prompt to history.jsonl before the transcript line,
// and deja's prompt hook runs a pass in between: a new session's first prompt
// is indexed from the log, and the transcript lands a pass later. The log's
// copy goes and the session is the transcript's, as a rebuild has it (#5046).
func TestATranscriptArrivingAfterItsPromptTakesTheSessionOver(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	h := filepath.Join(tmp, "history.jsonl")
	asked := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	parityWrite(t, h, claudeHistoryEntry(t, "new-1", "/tmp/proj", "fix the flaky pool test", asked), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	if n, _ := countPrompt(t, inc, "claude", "new-1", "fix the flaky pool test"); n != 1 {
		t.Fatalf("the log's prompt was not indexed before the transcript: %d", n)
	}
	tr := filepath.Join(tmp, "claude", "-tmp-proj", "new-1.jsonl")
	parityWrite(t, tr, parityClaudeLine("new-1", "00:00.5Z", "user", `"fix the flaky pool test"`)+
		parityClaudeLine("new-1", "00:05Z", "assistant", `"the conn was never closed"`), false)
	parityPass(t, inc, false)
	if n, path := countPrompt(t, inc, "claude", "new-1", "fix the flaky pool test"); n != 1 || path != tr {
		t.Errorf("after the transcript landed the prompt is held %d times, row %s", n, path)
	}
	sameAsRebuild(t, inc)
}

// The same pair in Codex: a rollout written after its history line.
func TestACodexRolloutArrivingAfterItsHistoryLineTakesItOver(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	const id = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0010"
	parityWrite(t, filepath.Join(tmp, "codex", "history.jsonl"), `{"session_id":"`+id+`","ts":1784278801,"text":"why does the retry loop spin"}`+"\n", false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	roll := filepath.Join(tmp, "codex", "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-"+id+".jsonl")
	parityWrite(t, roll, `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/tmp/proj"}}`+"\n"+
		`{"timestamp":"2026-07-17T09:00:01.437Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the retry loop spin"}]}}`+"\n"+
		`{"timestamp":"2026-07-17T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the backoff never resets"}]}}`+"\n", false)
	parityPass(t, inc, false)
	if n, path := countPrompt(t, inc, "codex", id, "why does the retry loop spin"); n != 1 || path != roll {
		t.Errorf("after the rollout landed the prompt is held %d times, row %s", n, path)
	}
	sameAsRebuild(t, inc)
}

// The other order: a transcript on disk first, its prompt logged later. The
// parser leaves the line to the transcript.
func TestAPromptLoggedAfterItsTranscriptIsLeftToIt(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	tr := filepath.Join(tmp, "claude", "-tmp-proj", "first-1.jsonl")
	parityWrite(t, tr, parityClaudeLine("first-1", "00:00Z", "user", `"why does the cache miss"`)+
		parityClaudeLine("first-1", "00:05Z", "assistant", `"the key carries the port"`), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	parityWrite(t, filepath.Join(tmp, "history.jsonl"), claudeHistoryEntry(t, "first-1", "/tmp/proj", "why does the cache miss", time.Date(2026, 7, 17, 9, 0, 0, 300_000_000, time.UTC)), false)
	parityPass(t, inc, false)
	if n, path := countPrompt(t, inc, "claude", "first-1", "why does the cache miss"); n != 1 || path != tr {
		t.Errorf("the prompt is held %d times, row %s", n, path)
	}
	sameAsRebuild(t, inc)
}

// A prompt the log holds a moment before its transcript waits a pass: the file
// is read again from the same offset, and by then the parser skips the line,
// so the common order never takes the full rewrite a takeover needs.
func TestAFreshPromptWaitsForItsTranscript(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude", "DEJA_CLAUDE_HISTORY_FILE": "history.jsonl"})
	h := filepath.Join(tmp, "history.jsonl")
	now := time.Now().UTC().Truncate(time.Second)
	inc := filepath.Join(tmp, "inc")
	// A store built first, so the passes below are appends.
	parityWrite(t, filepath.Join(tmp, "claude", "-tmp-proj", "old-1.jsonl"), parityClaudeLine("old-1", "00:00Z", "user", `"an older question"`), false)
	parityPass(t, inc, false)
	parityWrite(t, h, claudeHistoryEntry(t, "live-1", "/tmp/proj", "why does the queue stall", now), false)
	parityPass(t, inc, false)
	if _, ok, _ := FindByIdentity(inc, "claude", "live-1"); ok {
		t.Fatal("a prompt seconds old was indexed from the log before its transcript could land")
	}
	tr := filepath.Join(tmp, "claude", "-tmp-proj", "live-1.jsonl")
	line := fmt.Sprintf(`{"type":"user","sessionId":"live-1","timestamp":%q,"cwd":"/tmp/proj","message":{"role":"user","content":"why does the queue stall"}}`+"\n", now.Add(500*time.Millisecond).Format(time.RFC3339Nano))
	parityWrite(t, tr, line, false)
	parityPass(t, inc, false)
	if n, path := countPrompt(t, inc, "claude", "live-1", "why does the queue stall"); n != 1 || path != tr {
		t.Errorf("the prompt is held %d times, row %s", n, path)
	}
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := m.Files[h]; !ok || f.Size == 0 {
		t.Error("the log was not marked read once the transcript landed")
	}
	sameAsRebuild(t, inc)
}
