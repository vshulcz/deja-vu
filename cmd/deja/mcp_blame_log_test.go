package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja log` answers "what did deja put in front of the agent". blame hands
// over more than recall does — whole sessions rather than budgeted snippets —
// and left no trace at all (#682).
func TestBlameOverMCPIsRecorded(t *testing.T) {
	dir := seedBlameIndex(t)
	before := len(usage.Snapshots(dir, 100))
	beforeTotals := usage.Totals(dir)

	text, err := callMCPTool(dir, "blame", json.RawMessage(`{"path":"pool.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	after := usage.Snapshots(dir, 100)
	if len(after) != before+1 {
		t.Fatalf("snapshots %d -> %d, want one more", before, len(after))
	}
	last := after[0]
	if last.Kind != usage.KindBlame {
		t.Errorf("kind = %q, want %q", last.Kind, usage.KindBlame)
	}
	// The recorded size has to be what the agent actually received, or the log
	// is a story about a different call.
	if last.Bytes != len(text) {
		t.Errorf("recorded %d bytes, handed over %d", last.Bytes, len(text))
	}
	if last.Digest != text {
		t.Errorf("digest is not what was handed over:\n%q\n%q", last.Digest, text)
	}
	// Two records, two readers: the snapshot carries the text, the usage log
	// carries the counters the statusline and `deja stats` add up.
	afterTotals := usage.Totals(dir)
	if afterTotals.Recalls != beforeTotals.Recalls+1 {
		t.Errorf("usage recalls %d -> %d, want one more", beforeTotals.Recalls, afterTotals.Recalls)
	}
	if afterTotals.Bytes != beforeTotals.Bytes+len(text) {
		t.Errorf("usage bytes %d -> %d, handed over %d", beforeTotals.Bytes, afterTotals.Bytes, len(text))
	}
	if last.Sessions == 0 {
		t.Errorf("recorded 0 sessions for an answer naming one: %q", text)
	}
}

// seedBlameIndex builds a store whose sessions name a file in their speech,
// which is what blame matches on.
func seedBlameIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-b")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"b1","cwd":"/w/b","timestamp":"2026-07-11T10:00:00Z","message":{"role":"user","content":"we rewrote pool.go to fix the starvation"}}` + "\n" +
		`{"type":"assistant","sessionId":"b1","cwd":"/w/b","timestamp":"2026-07-11T10:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"capped the pool at 200"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "b1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
