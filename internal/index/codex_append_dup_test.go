package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A rollout that repeats a message verbatim — same role, same timestamp, same
// text — is deduped by a full build and was not by the append path, so the same
// file held 3 records or 2 depending on how it entered the index. An archive
// checked against a rebuilt copy of itself then compared unequal (#3934).
func TestAppendDedupesARepeatedMessageLikeARebuild(t *testing.T) {
	const dup = `{"timestamp":"2026-06-01T05:00:00.000Z","type":"session_meta","payload":{"id":"dup-1","cwd":"/w/x"}}
{"timestamp":"2026-06-01T05:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same words"}]}}
{"timestamp":"2026-06-01T05:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same words"}]}}
{"timestamp":"2026-06-01T05:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
`
	const other = `{"timestamp":"2026-06-01T04:00:00.000Z","type":"session_meta","payload":{"id":"other-1","cwd":"/w/x"}}
{"timestamp":"2026-06-01T04:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"other session"}]}}
`
	count := func(t *testing.T, incremental bool) int {
		t.Helper()
		tmp := t.TempDir()
		root := filepath.Join(tmp, "codex", "sessions", "x")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		setHome(t, filepath.Join(tmp, "home"))
		t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
		t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
		t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
		t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
		t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
		t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
		t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
		t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
		dupPath := filepath.Join(root, "rollout-2026-06-01T05-00-00-dup-1.jsonl")
		dir := filepath.Join(tmp, "index.db")
		if incremental {
			// The index exists first and the rollout arrives afterwards, which
			// is the path a machine actually takes.
			if err := os.WriteFile(filepath.Join(root, "rollout-2026-06-01T04-00-00-other-1.jsonl"), []byte(other), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(dir, "codex", false, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(dupPath, []byte(dup), 0o644); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Second)
		_ = os.Chtimes(dupPath, now, now)
		if err := Ensure(dir, "codex", false, nil); err != nil {
			t.Fatal(err)
		}
		recs, err := ReadRecords(dir)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range recs {
			if r.Record.Text == "same words" {
				n++
			}
		}
		return n
	}
	fresh := count(t, false)
	appended := count(t, true)
	if fresh != 1 {
		t.Fatalf("a full build kept %d copies of the repeated message, want 1", fresh)
	}
	if appended != fresh {
		t.Errorf("append kept %d copies, a rebuild %d — the same file indexes differently", appended, fresh)
	}
}
