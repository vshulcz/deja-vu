package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bigGrokUpdates writes a session shaped like the one in #1321: a few thousand
// ACP lines, most of them tool events carrying large payloads.
func bigGrokUpdates(t *testing.T, lines int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions", "proj", "01a00feb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"01a00feb","cwd":"/work/app"},"generated_title":"Fix the parser","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("build output line with paths and errors ", 100)
	var b strings.Builder
	for i := 0; i < lines; i++ {
		switch i % 20 {
		case 0:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"question %d about the build"},"_meta":{"promptIndex":%d}}}}`+"\n", 1782900000+i, i, i)
		case 1:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer %d"}},"_meta":{"promptId":"p%d"}}}`+"\n", 1782900000+i, i, i)
		default:
			fmt.Fprintf(&b, `{"timestamp":%d,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"call_%d","title":"bash","kind":"execute","status":"completed","content":[{"type":"content","content":{"type":"text","text":%q}}]}}}`+"\n", 1782900000+i, i, payload)
		}
	}
	p := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err == nil {
		t.Logf("fixture: %d lines, %.1f MB", lines, float64(fi.Size())/(1<<20))
	}
	return p
}

// The session shape from #1321: 2955 ACP lines, ~11 MB, mostly tool events.
// Indexing the run is the point; the parser filtered tool lines out before
// decoding them precisely because they are most of the file, so the cost of
// keeping them is worth stating and worth watching.
func TestGrokIndexesTheRunAtAKnownCost(t *testing.T) {
	p := bigGrokUpdates(t, 2955)
	started := time.Now()
	ss, err := ParseGrokFile(p)
	took := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{}
	for _, s := range ss {
		for _, m := range s.Messages {
			roles[m.Role]++
		}
	}
	t.Logf("parsed 11 MB in %v; roles=%v", took, roles)
	if roles[RoleToolOutput] == 0 {
		t.Error("the run is not indexed: no tool records from a session that is mostly tool events")
	}
	// Measured at ~170 ms on this fixture against ~10 ms when the tool stream was
	// skipped: reading and decoding 11 MB instead of stepping over most of it.
	// Half a second leaves room for a slower machine while still catching a
	// parser that has started doing something per-line-squared.
	if took > 500*time.Millisecond {
		t.Errorf("parsing took %v, which is more than reading 11 MB costs", took)
	}
}
