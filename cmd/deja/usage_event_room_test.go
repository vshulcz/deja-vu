package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `usage.EventRoom` is how large one event may be for the log's own rotation to
// leave it under its threshold — the relationship `internal/usage` states. This
// is the half that holds the writers to it: a real recall over a store with
// many sessions, whose ids are what makes an event grow.
func TestARealEventFitsItsRoom(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Shaped for the worst case rather than around it: an event grows with the
	// number of ids, an answer holds more sessions when each says less, and an
	// id is as long as a filename may be. Long turns would cut the answer to a
	// handful of sessions and measure nothing.
	for i := 0; i < 60; i++ {
		id := strings.Repeat("a", 246) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
		seedClaude(t, claude, "app", id, "pgbouncer timed out", "we retried")
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := callMCPTool(index.DefaultDir(), "recall", json.RawMessage(`{"query":"pgbouncer","limit":100}`)); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(usage.Path(index.DefaultDir()))
	if err != nil {
		t.Fatal(err)
	}
	var widest int
	var carried int
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e usage.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if len(line) > widest {
			widest, carried = len(line), len(e.SessionIDs)
		}
	}
	if widest == 0 {
		t.Fatal("nothing was recorded, so this proves nothing")
	}
	// The ids are what grows, so the count is what the failure has to name: the
	// number of sessions seeded says nothing about how many the answer held.
	if carried < 2 {
		t.Fatalf("the widest event carried %d ids, so it measures nothing", carried)
	}
	if widest > usage.EventRoom {
		t.Errorf("a recall wrote an event of %d bytes against %d of room, carrying %d ids of 248 characters",
			widest, usage.EventRoom, carried)
	}
	t.Logf("widest event: %d bytes of %d, carrying %d ids", widest, usage.EventRoom, carried)
}
