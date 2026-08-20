package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The injection log records what went out; without who received it, the only
// way to tell whether a recall was used is the sentence the block asks the
// agent to say — measured on a real store, that follows 22 of 1218 injections.
// The hook knows the agent session from its own payload, so it writes it down.
func TestHookPromptRecordsTheSessionItAnswered(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "intoterm", []string{
		`{"type":"user","sessionId":"intoterm","timestamp":"` + old +
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

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"ses_from_harness"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "transaction mode") {
		t.Fatalf("the hook said nothing, so there is no injection to record:\n%q", out.String())
	}

	b, err := os.ReadFile(usage.SnapshotPath(index.DefaultDir()))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var snap usage.Snapshot
		if json.Unmarshal([]byte(line), &snap) != nil {
			continue
		}
		if snap.Kind == usage.KindDejaVu && snap.Into == "ses_from_harness" {
			found = true
		}
	}
	if !found {
		t.Errorf("the injection log does not say which agent session got the block:\n%s", b)
	}
}
