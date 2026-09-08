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

// Nobody asked. A harness delivers its own plumbing as the next user turn — a
// finished background task, a system reminder — and this hook fired on it like
// a question. The terms were the envelope's field names plus a task id that
// exists nowhere else, and what they matched was another notification in
// another session: noise injected as recalled history, under a line telling the
// user that deja fires on noise (#3156).
func TestHookPromptStandsDownOnHarnessArtifacts(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	// A past session holding a notification of its own — what the noisy prompt
	// matched — and a real answer beside it, so silence here is a decision and
	// not an empty store.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "past.jsonl"), "past", []string{
		`{"type":"user","sessionId":"past","timestamp":"` + ts +
			`","message":{"role":"user","content":"<task-notification>\n<task-id>zx91qq4kb</task-id>\n<status>failed</status>\n</task-notification>"}}`,
		`{"type":"assistant","sessionId":"past","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"the zibblex retry cap is four, we settled that"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	for _, artifact := range []string{
		`<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n</task-notification>`,
		`<system-reminder>The task list is empty.</system-reminder>`,
		`Summary: 1. Primary Request and Intent:\n   keep polishing the tool\n2. Key Technical Concepts:\n   - hooks`,
	} {
		var out bytes.Buffer
		in := strings.NewReader(`{"prompt":"` + artifact + `","session_id":"asking"}`)
		if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "deja-recall") || strings.Contains(out.String(), "you have been here") {
			t.Errorf("the hook recalled on a harness artifact:\nprompt: %s\ngot: %q", artifact, out.String())
		}
	}

	// The control: a real question in the same session, against the same
	// store, still gets its block.
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we decide about the zibblex retry cap","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "retry cap is four") {
		t.Fatalf("a real question went unanswered, so the silence above proves nothing:\n%q", out.String())
	}
}
