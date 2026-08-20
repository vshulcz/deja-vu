package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A session too long to read as one episode is narrowed to the part that
// matched, not dropped and not injected whole. Turning the narrowing off left
// the whole suite green: the only test that watches focusCalls wants it at
// zero, which a disabled narrowing satisfies for the wrong reason.
func TestHookPromptNarrowsAMarathonItShows(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	lines := make([]string, 0, dejaVuMaxMessages+3)
	for i := 0; i < dejaVuMaxMessages+1; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"assistant","sessionId":"marathon","timestamp":"%s","message":{"role":"assistant","content":"routine step %d, nothing to see"}}`,
			ts, i))
	}
	lines = append(lines, `{"type":"assistant","sessionId":"marathon","timestamp":"`+ts+
		`","message":{"role":"assistant","content":"the fix: kestrel retries are capped at four"}}`)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "marathon.jsonl"), "marathon", lines)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	focusCalls = 0
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we decide about kestrel","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "capped at four") {
		t.Fatalf("the marathon's answer was not recalled:\n%q", out.String())
	}
	if focusCalls != 1 {
		t.Errorf("narrowed %d session(s); a marathon that gets shown must be narrowed first", focusCalls)
	}
	// Only the quoted lines matter: the credit line names the session by its
	// opening message, which is noise by construction here.
	for _, ln := range strings.Split(out.String(), "\n") {
		quoted := strings.TrimSpace(ln)
		if !strings.HasPrefix(quoted, "- User:") && !strings.HasPrefix(quoted, "- Assistant:") {
			continue
		}
		if strings.Contains(quoted, "routine step") {
			t.Errorf("the block quotes the marathon's noise:\n%q", quoted)
		}
	}
}

// One ordinary word is a hint, not an answer: it earns a pointer, and the
// pointer is what keeps a whole digest off every message. Making the payload
// unconditional — never a pointer — left the suite green.
func TestHookPromptPointsRatherThanQuotesOnOneOrdinaryWord(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	// Two sessions so the matched word is ordinary rather than rare, and the
	// question shares exactly that one word with them.
	for i, name := range []string{"one", "two"} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", name+".jsonl"), name, []string{
			fmt.Sprintf(`{"type":"user","sessionId":"%s","timestamp":"%s","message":{"role":"user","content":"the migration ran fine on shard %d"}}`, name, ts, i),
			fmt.Sprintf(`{"type":"assistant","sessionId":"%s","timestamp":"%s","message":{"role":"assistant","content":"migration %d finished, nothing else changed"}}`, name, ts, i),
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"is the migration something we should worry about","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "deja-recall") {
		t.Skip("nothing was recalled at all, so this says nothing about the payload rule")
	}
	if strings.Contains(got, "- User:") || strings.Contains(got, "- Assistant:") {
		t.Errorf("one ordinary word bought a full digest:\n%q", got)
	}
	if !strings.Contains(got, "call recall") {
		t.Errorf("the pointer text is missing:\n%q", got)
	}
}
