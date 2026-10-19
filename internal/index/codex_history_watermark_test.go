package index

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func codexHistoryLine(id string, at time.Time, text string) string {
	return hjson(map[string]any{"session_id": id, "ts": at.Unix(), "text": text})
}

// A pass that reads Codex's history.jsonl beside a new rollout used to read
// the log whole and replace every session known from it alone: 9 changed
// files counted as 257 sessions replaced (#5403). Read from where the last
// pass stopped, only the session with a new line is replaced, and the index
// still matches a rebuild.
func TestCodexHistoryReplacesOnlyTheSessionsWithNewLines(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	hist := filepath.Join(tmp, "codex", "history.jsonl")
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	var body strings.Builder
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("019a0000-0000-7000-8000-%012d", i)
		body.WriteString(codexHistoryLine(id, base.Add(time.Duration(i)*time.Minute), fmt.Sprintf("why does the ledger %d retry", i)))
		body.WriteString(codexHistoryLine(id, base.Add(time.Duration(i)*time.Minute+time.Second), "and the outbox too"))
	}
	parityWrite(t, hist, body.String(), false)
	rollout := func(id string, day int) string {
		return filepath.Join(tmp, "codex", "sessions", "2026", "06", fmt.Sprintf("%02d", day), fmt.Sprintf("rollout-2026-06-%02dT10-00-00-%s.jsonl", day, id))
	}
	at := base.Add(48 * time.Hour)
	parityWrite(t, rollout("019a1111-0000-7000-8000-000000000001", 3),
		codexLine(at, "session_meta", map[string]any{"id": "019a1111-0000-7000-8000-000000000001", "cwd": "/w/app"})+
			codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "why does the pool deadlock"}}}), false)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)

	// One more prompt for one history-only session, and the rollout of
	// another arriving: a takeover, which only the replacement path does.
	parityWrite(t, hist, codexHistoryLine("019a0000-0000-7000-8000-000000000007", base.Add(time.Hour), "the kafka webhook again"), true)
	at = base.Add(72 * time.Hour)
	parityWrite(t, rollout("019a0000-0000-7000-8000-000000000005", 4),
		codexLine(at, "session_meta", map[string]any{"id": "019a0000-0000-7000-8000-000000000005", "cwd": "/w/svc"})+
			codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "why does the schema migration hang"}}}), false)
	var out bytes.Buffer
	if err := Ensure(inc, "", false, &out); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(\d+) sessions? replaced`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no replacement pass ran, so this measures nothing:\n%s", out.String())
	}
	if m[1] != "2" {
		t.Errorf("sessions replaced = %s, want 2 (the one with a new prompt and the one its rollout took over):\n%s", m[1], out.String())
	}
	if !strings.Contains(out.String(), "2 changed transcripts") {
		t.Errorf("want the history and the new rollout read:\n%s", out.String())
	}
	sameAsRebuild(t, inc)

	// Codex trims history.jsonl from the front once it passes max_bytes: the
	// prefix no longer matches, and the log is read whole again.
	trimmed := strings.SplitAfterN(body.String(), "\n", 5)[4] +
		codexHistoryLine("019a0000-0000-7000-8000-000000000007", base.Add(time.Hour), "the kafka webhook again") +
		codexHistoryLine("019a0000-0000-7000-8000-000000000009", base.Add(2*time.Hour), "and the tokenizer")
	parityWrite(t, hist, trimmed, false)
	parityPass(t, inc, false)
	sameAsRebuild(t, inc)
}

// The history parse skips a session whose rollout is on disk, so a session
// can leave the parse or come back to it with no new line: a rollout that
// holds no message yet arriving for a logged session, or one deleted. Each
// pass still matches a rebuild.
func TestCodexHistoryTailFollowsRolloutsThatComeAndGo(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex"})
	hist := filepath.Join(tmp, "codex", "history.jsonl")
	a, b := "019a0000-0000-7000-8000-00000000000a", "019a0000-0000-7000-8000-00000000000b"
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	roll := filepath.Join(tmp, "codex", "sessions", "2026", "06", "03", "rollout-2026-06-03T10-00-00-"+a+".jsonl")
	meta := codexLine(base.Add(48*time.Hour), "session_meta", map[string]any{"id": a, "cwd": "/w/app"})

	t.Run("a rollout with no message yet arrives", func(t *testing.T) {
		parityWrite(t, hist, codexHistoryLine(a, base, "why does the ledger retry")+codexHistoryLine(b, base.Add(time.Minute), "and the outbox"), false)
		inc := filepath.Join(t.TempDir(), "inc")
		parityPass(t, inc, false)
		parityWrite(t, hist, codexHistoryLine(b, base.Add(2*time.Minute), "the outbox again"), true)
		parityWrite(t, roll, meta, false)
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
		parityWrite(t, roll, codexLine(base.Add(49*time.Hour), "response_item", map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "why does the pool deadlock"}}}), true)
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
	})

	// The same with the log left alone in the pass that moves the session.
	t.Run("a rollout with no message yet arrives beside an untouched log", func(t *testing.T) {
		parityWrite(t, hist, codexHistoryLine(a, base, "why does the ledger retry")+codexHistoryLine(b, base.Add(time.Minute), "and the outbox"), false)
		inc := filepath.Join(t.TempDir(), "inc")
		parityPass(t, inc, false)
		parityWrite(t, roll, meta, false)
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
	})
	t.Run("an empty rollout goes beside an untouched log", func(t *testing.T) {
		parityWrite(t, hist, codexHistoryLine(a, base, "why does the ledger retry")+codexHistoryLine(b, base.Add(time.Minute), "and the outbox"), false)
		parityWrite(t, roll, meta, false)
		inc := filepath.Join(t.TempDir(), "inc")
		parityPass(t, inc, false)
		if err := os.Remove(roll); err != nil {
			t.Fatal(err)
		}
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
	})
	t.Run("an empty rollout goes", func(t *testing.T) {
		parityWrite(t, hist, codexHistoryLine(a, base, "why does the ledger retry")+codexHistoryLine(b, base.Add(time.Minute), "and the outbox"), false)
		parityWrite(t, roll, meta, false)
		inc := filepath.Join(t.TempDir(), "inc")
		parityPass(t, inc, false)
		if err := os.Remove(roll); err != nil {
			t.Fatal(err)
		}
		parityWrite(t, hist, codexHistoryLine(b, base.Add(2*time.Minute), "the outbox again"), true)
		parityPass(t, inc, false)
		sameAsRebuild(t, inc)
	})
}
