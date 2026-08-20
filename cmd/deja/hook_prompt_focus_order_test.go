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

// Narrowing a candidate walks every message it holds, and the session being
// written is the longest one on a real store — measured there, doing it before
// the two cheap discards cost 45 ms of the hook's 181. The output is the same
// either way, so only a count can hold the order in place.
func TestHookPromptDoesNotNarrowASessionItWillDiscard(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)

	// Long enough to be narrowed, and it is the session asking.
	lines := make([]string, 0, dejaVuMaxMessages+2)
	for i := 0; i < dejaVuMaxMessages+1; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"assistant","sessionId":"selflong","timestamp":"%s","message":{"role":"assistant","content":"kestrel notes %d"}}`,
			ts, i))
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "selflong.jsonl"), "selflong", lines)
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
	in := strings.NewReader(`{"prompt":"what did we decide about kestrel","session_id":"selflong"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "kestrel notes") {
		t.Fatalf("the session being written was recalled to itself:\n%q", out.String())
	}
	if focusCalls != 0 {
		t.Errorf("narrowed %d session(s) that were then discarded — the cheap checks must come first", focusCalls)
	}
}
