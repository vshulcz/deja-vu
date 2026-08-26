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

	// Sixty sessions, each with a name as long as the longest a real store
	// produces — a codex or gemini transcript filename.
	long := strings.Repeat("the pgbouncer pool kept timing out in transaction mode ", 4)
	for i := 0; i < 60; i++ {
		id := strings.Repeat("a", 60) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
		seedClaude(t, claude, "app", id, "turn "+long, "reply "+long)
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
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if len(line) > widest {
			widest = len(line)
		}
	}
	if widest == 0 {
		t.Fatal("nothing was recorded, so this proves nothing")
	}
	if widest > usage.EventRoom {
		t.Errorf("a recall wrote an event of %d bytes against %d of room: %d ids at 62 characters",
			widest, usage.EventRoom, 60)
	}
	t.Logf("widest event: %d bytes of %d", widest, usage.EventRoom)
}
