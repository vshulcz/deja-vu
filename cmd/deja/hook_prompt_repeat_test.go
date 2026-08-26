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

// The repetition #2038 measured, at the level it actually happens: two agent
// sessions asking the same thing in the same project. The cooldown used to be
// keyed on the agent session id alone, so the second one started blank and was
// handed the same past session again — 92% of per-prompt injections in six
// weeks of a real log were repeats of 74 sessions.
//
// End-to-end rather than on the helpers, because the helpers passed with the
// cooldown unwired.
func TestASecondAgentSessionIsNotHandedTheSameMemory(t *testing.T) {
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

	ask := func(agentSession string) string {
		t.Helper()
		var out bytes.Buffer
		in := strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"` + agentSession + `"}`)
		if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	first := ask("agent-one")
	if !strings.Contains(first, "transaction mode") {
		t.Fatalf("the first agent session got no memory, so there is nothing to repeat:\n%q", first)
	}

	second := ask("agent-two")
	if strings.Contains(second, "transaction mode") {
		t.Errorf("a second agent session in the same project was handed the same session again:\n%q", second)
	}
}
