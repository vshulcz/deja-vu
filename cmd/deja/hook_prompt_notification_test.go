package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A background job finishing arrives as a user turn: a banner saying it is not
// the user, the host's own paragraph, then the notification block. Taking the
// block out left the paragraph, and the hook answered it — "you have been here"
// about a sentence no person wrote (#3156 fixed the block, not the prose).
func TestHookPromptSaysNothingToAHostNotification(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "wombat.jsonl"), "wombat", []string{
		`{"type":"user","sessionId":"wombat","timestamp":"` + ts +
			`","message":{"role":"user","content":"the automated background task never received a message from the user"}}`,
		`{"type":"assistant","sessionId":"wombat","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"the wombat queue drops a notification whose status arrives last"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	turn := "[SYSTEM NOTIFICATION - NOT USER INPUT]\\n" +
		"This is an automated background-task event, NOT a message from the user.\\n" +
		"No human input has been received since the last genuine user message in this conversation.\\n\\n" +
		"<task-notification>\\n<task-id>br50mykp6</task-id>\\n<status>completed</status>\\n</task-notification>"

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"` + turn + `","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "" {
		t.Errorf("the hook answered the host talking to itself:\n%s", got)
	}
}
