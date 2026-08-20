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

// A session already injected into this agent session is discarded on its id.
// Handing that id to the ranking means it is never read: measured on a real
// store, 15 of the 26 candidates a prompt ranks are ones it has already shown,
// and reading them cost 40-56 ms a message. Walking one session, that is the
// difference between 65 ms and 26 ms a message.
//
// The block that comes out is the same either way, so the count of narrowed
// sessions is what holds this in place.
func TestHookPromptDoesNotReadASessionItAlreadyShowed(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	lines := make([]string, 0, dejaVuMaxMessages+2)
	for i := 0; i < dejaVuMaxMessages+1; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"assistant","sessionId":"longone","timestamp":"%s","message":{"role":"assistant","content":"the fix: kestrel retries are capped at four, note %d"}}`,
			ts, i))
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "longone.jsonl"), "longone", lines)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var first bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we decide about kestrel","session_id":"agent-1"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &first, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "kestrel") {
		t.Fatalf("the session was not recalled the first time, so the second ask proves nothing:\n%q", first.String())
	}

	rankedAlreadyShown = 0
	var second bytes.Buffer
	in = strings.NewReader(`{"prompt":"and what about kestrel retries","session_id":"agent-1"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &second, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second.String(), "capped at four") {
		t.Fatalf("the same session was injected twice:\n%q", second.String())
	}
	if rankedAlreadyShown != 0 {
		t.Errorf("the ranking returned %d session(s) already shown — they are read from disk in full and then dropped", rankedAlreadyShown)
	}
}
