package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// #682 put blame in the log because it was the largest thing the server handed
// over unrecorded. resources/read hands over the same whole session in the same
// frame recall_context uses, and recorded nothing: three reads, 2058 bytes of
// transcript to the agent, `deja log --json` still `[]`.
func TestMCPResourceReadIsLogged(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "res1.jsonl"), "res1", []string{
		`{"type":"user","sessionId":"res1","timestamp":"2026-05-01T10:00:00Z","message":{"role":"user","content":"the wibblefish index went stale"}}`,
		`{"type":"assistant","sessionId":"res1","timestamp":"2026-05-01T10:01:00Z","message":{"role":"assistant","content":"rebuilt it, wibblefish is fine"}}`,
	})
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	read, code, msg := mcpResourceRead(dir, "deja://session/claude:res1")
	if code != 0 {
		t.Fatalf("resources/read: %d %s", code, msg)
	}
	contents, _ := read.(map[string]any)["contents"].([]map[string]any)
	if len(contents) != 1 {
		t.Fatalf("resources/read returned %d contents", len(contents))
	}
	text, _ := contents[0]["text"].(string)
	if len(text) == 0 {
		t.Fatal("resources/read served an empty digest")
	}

	events := usage.Events(dir, 10)
	var got *usage.Event
	for i := range events {
		if events[i].Kind == usage.KindResource {
			got = &events[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("resources/read served %d bytes and left no log entry; log has %+v", len(text), events)
	}
	if got.Bytes != len(text) {
		t.Errorf("logged %d bytes, served %d", got.Bytes, len(text))
	}
	if got.Sessions != 1 {
		t.Errorf("logged %d sessions, want 1", got.Sessions)
	}
	if len(got.SessionIDs) != 1 || got.SessionIDs[0] != "res1" {
		t.Errorf("logged ids %v, want [res1]", got.SessionIDs)
	}
}
