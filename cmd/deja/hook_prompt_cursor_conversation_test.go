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

// Cursor's beforeSubmitPrompt names the conversation conversation_id and sends
// no session_id. Read as no session at all, the cooldown never recorded and
// the same block went out on every prompt of the conversation (#3287).
func TestCursorConversationIDIsTheSession(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "repeatterm", []string{
		`{"type":"user","sessionId":"repeatterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	ask := func(conversation string) string {
		t.Helper()
		var out bytes.Buffer
		in := strings.NewReader(`{"conversation_id":"` + conversation + `","prompt":"do we need pgbouncer here","workspace_roots":["` + jsonEscaped(t, cwd) + `"]}`)
		if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if first := ask("conv-x"); !strings.Contains(first, "transaction mode") {
		t.Fatalf("the first prompt got no memory, so there is nothing to repeat:\n%q", first)
	}
	if again := ask("conv-x"); strings.Contains(again, "transaction mode") {
		t.Errorf("the same conversation was handed the same session again:\n%q", again)
	}
}
