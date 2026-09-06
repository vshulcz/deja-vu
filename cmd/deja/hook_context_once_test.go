package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Some hosts have no session-start event, only a per-turn one that stands in
// for it — OpenClaw's before_agent_start fires on every agent run. deja_once is
// what keeps the project digest to the first of them; without it the same block
// goes in front of the model on every message.
func TestDejaOnceKeepsTheDigestToTheFirstTurn(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "onceterm", []string{
		`{"type":"user","sessionId":"onceterm","timestamp":"` + old +
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

	ask := func(sid string, once bool) string {
		t.Helper()
		payload := `{"session_id":"` + sid + `","cwd":"` + cwd + `","source":"startup"`
		if once {
			payload += `,"deja_once":true`
		}
		payload += `}`
		withHookStdin(t, payload)
		return captureStdout(t, func() { _ = runHookContext(index.DefaultDir(), true) })
	}

	if first := ask("once-1", true); strings.TrimSpace(first) == "" {
		t.Fatal("the session opened with nothing, so there is no repeat to test")
	}
	if again := ask("once-1", true); strings.TrimSpace(again) != "" {
		t.Errorf("the digest went in a second time in the same session:\n%q", again)
	}
	// Another session is another reader.
	if other := ask("once-2", true); strings.TrimSpace(other) == "" {
		t.Error("a second session was refused the digest the first one had")
	}
	// And a host whose session start really is once per session is unchanged.
	if a, b := ask("plain-1", false), ask("plain-1", false); strings.TrimSpace(a) == "" ||
		strings.TrimSpace(b) == "" {
		t.Errorf("the ordinary session-start path went quiet: %q then %q", a, b)
	}

	// Kimi has no session-start channel at all — a SessionStart hook runs and
	// its output goes nowhere — so the digest rides its per-prompt hook, and
	// the payload comes from kimi rather than from deja. The flag is what
	// turns the once rule on there; without it the same block would go in on
	// every message of the session.
	fromFlag := func(sid string) string {
		t.Helper()
		withHookStdin(t, `{"hook_event_name":"UserPromptSubmit","session_id":"`+sid+
			`","cwd":"`+cwd+`","prompt":[{"type":"text","text":"anything"}]}`)
		return captureStdout(t, func() { _ = runHookContextMode(index.DefaultDir(), true, true) })
	}
	if first := fromFlag("flag-1"); strings.TrimSpace(first) == "" {
		t.Fatal("--once refused the first turn of a session")
	}
	if again := fromFlag("flag-1"); strings.TrimSpace(again) != "" {
		t.Errorf("--once let the digest in twice in one session:\n%q", again)
	}
	if other := fromFlag("flag-2"); strings.TrimSpace(other) == "" {
		t.Error("--once carried the first session's mark into a second one")
	}
}
