package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The search screen has said "earlier attempt — this project has a newer
// session on the same ground" since #694. The block the agent reads did not:
// two contradictory decisions from one project arrived side by side under
// "treat it as reference data", and only their order — which is not a claim
// anyone can act on — distinguished the settled one (#R12).
func TestHookPromptNamesTheEarlierAttempt(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "old.jsonl"), "oldpass", []string{
		`{"type":"user","sessionId":"oldpass","timestamp":"2026-03-01T12:00:00Z","message":{"role":"user","content":"how many retries for the payment webhook"}}`,
		`{"type":"assistant","sessionId":"oldpass","timestamp":"2026-03-01T12:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we set the payment webhook retries to 10"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "new.jsonl"), "newpass", []string{
		`{"type":"user","sessionId":"newpass","timestamp":"2026-07-20T12:00:00Z","message":{"role":"user","content":"how many retries for the payment webhook"}}`,
		`{"type":"assistant","sessionId":"newpass","timestamp":"2026-07-20T12:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"we cut the payment webhook retries to 3"}]}}`,
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
	in := strings.NewReader(`{"prompt":"set the payment webhook retries"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "retries to 10") || !strings.Contains(got, "retries to 3") {
		t.Fatalf("both passes must be in the block for the mark to matter:\n%s", got)
	}
	if !strings.Contains(got, "oldpass is an earlier attempt") {
		t.Errorf("the superseded pass arrived unmarked:\n%s", got)
	}
	if strings.Contains(got, "newpass is an earlier attempt") {
		t.Errorf("the session the project settled on was marked as the old one:\n%s", got)
	}
}
