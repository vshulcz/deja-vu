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

// Compaction throws away the blocks a session was shown; the list that stops
// them repeating used to outlive them, so the memory the agent had just lost
// was exactly the memory recall refused to send again.
func TestPrecompactForgetsWhatThisSessionWasShown(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "compactterm", []string{
		`{"type":"user","sessionId":"compactterm","timestamp":"` + old +
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

	payload := `{"prompt":"do we need pgbouncer here","session_id":"agent-1"}`
	var first bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &first, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "transaction mode") {
		t.Fatalf("nothing was recalled to begin with:\n%q", first.String())
	}

	// Another session's entry, which compaction here must leave alone.
	f, err := os.OpenFile(index.DefaultDir()+".hookseen", os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("agent-2 someone-elses-block\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	withHookStdin(t, `{"session_id":"agent-1","hook_event_name":"PreCompact"}`)
	runHookPrecompact(index.DefaultDir())

	if got := alreadyInjected(index.DefaultDir(), "agent-1"); len(got) != 0 {
		t.Errorf("compaction left this session's seen list behind: %v", got)
	}
	if got := alreadyInjected(index.DefaultDir(), "agent-2"); !got["someone-elses-block"] {
		t.Error("compaction in one session cleared another session's entries")
	}

	var second bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &second, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "transaction mode") {
		t.Errorf("after compaction the memory was still withheld:\n%q", second.String())
	}
}

// A payload without a session id must not wipe the file: a harness that sends
// no id would otherwise clear every session's dedupe on its first compaction.
func TestPrecompactWithoutASessionIDKeepsTheList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.WriteFile(dir+".hookseen", []byte("agent-1 block-a\nagent-2 block-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forgetInjected(dir, "")
	for sid, block := range map[string]string{"agent-1": "block-a", "agent-2": "block-b"} {
		if !alreadyInjected(dir, sid)[block] {
			t.Errorf("%s lost %s to a compaction that named no session", sid, block)
		}
	}
}
