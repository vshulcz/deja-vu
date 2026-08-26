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

// snapshotOnlyInto is the one receiver a kind recorded. Every row of that kind
// has to agree: keeping the last would let a good row cover a bad one written
// before it.
func snapshotOnlyInto(t *testing.T, dir, kind string) string {
	t.Helper()
	b, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		t.Fatalf("nothing was written to the injection log: %v", err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var snap usage.Snapshot
		if json.Unmarshal([]byte(line), &snap) != nil {
			t.Fatalf("the injection log has a line deja cannot read back: %s", line)
		}
		if snap.Kind == kind {
			seen[snap.Into] = true
		}
	}
	switch len(seen) {
	case 0:
		t.Fatalf("the injection log has no %s row at all:\n%s", kind, b)
	case 1:
		for into := range seen {
			return into
		}
	}
	t.Fatalf("%s rows disagree on where they went: %v", kind, seen)
	return ""
}

// The session-start hook is the commonest injection there is, and it recorded
// no receiver at all while holding the id — pinned only at the usage package's
// own door, so the hook could stop passing it and nothing would say so (#1949).
// The déjà-vu path has had TestHookPromptRecordsTheSessionItAnswered since the
// field was added; this is its other half.
func TestHookContextRecordsTheSessionItStarted(t *testing.T) {
	skipWindowsEmptySessionID(t)
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "startterm", []string{
		`{"type":"user","sessionId":"startterm","timestamp":"` + old +
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
	// The hook exports the payload's cwd into the process, so without pinning it
	// here every later test in this package inherits a project directory that no
	// longer exists — and the second run below would keep this one's.
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	withHookStdin(t, `{"source":"startup","session_id":"ses_from_harness","cwd":"`+cwd+`"}`)
	if out := captureStdout(t, func() { runHookContextPlain(t) }); !strings.Contains(out, "transaction mode") {
		t.Fatalf("the hook injected nothing, so there is no record to check:\n%q", out)
	}

	if into := snapshotOnlyInto(t, index.DefaultDir(), usage.KindHook); into != "ses_from_harness" {
		t.Errorf("the session-start injection went to %q, not to the session in the payload", into)
	}
}

// Both hooks write the same field, and a reader pairing an injection with what
// the agent did next can only do it if they mean the same string. They take it
// from the same key of their own payloads, which is the part worth holding: an
// id one hook derived some other way would look right in the log and pair
// wrong.
func TestBothHooksRecordTheSameSessionForOneSession(t *testing.T) {
	skipWindowsEmptySessionID(t)
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "bothterm", []string{
		`{"type":"user","sessionId":"bothterm","timestamp":"` + old +
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
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	withHookStdin(t, `{"source":"startup","session_id":"ses_both","cwd":"`+cwd+`"}`)
	if out := captureStdout(t, func() { runHookContextPlain(t) }); !strings.Contains(out, "transaction mode") {
		t.Fatalf("the session-start hook injected nothing:\n%q", out)
	}
	var prompt bytes.Buffer
	in := strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"ses_both"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &prompt, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.String(), "transaction mode") {
		t.Fatalf("the prompt hook injected nothing:\n%q", prompt.String())
	}

	start := snapshotOnlyInto(t, index.DefaultDir(), usage.KindHook)
	dejaVu := snapshotOnlyInto(t, index.DefaultDir(), usage.KindDejaVu)
	if start != dejaVu {
		t.Errorf("one session was recorded under two names: session start wrote %q, deja vu wrote %q", start, dejaVu)
	}
	if start != "ses_both" {
		t.Errorf("neither hook recorded the session the harness named: %q", start)
	}
}

// runHookContextPlain runs the session-start hook. Stdout is captured by
// captureStdout around it, which drains the pipe while the hook writes — a
// digest larger than the pipe buffer would otherwise wedge the test.
func runHookContextPlain(t *testing.T) {
	t.Helper()
	if err := runHookContext(index.DefaultDir(), true); err != nil {
		t.Error(err)
	}
}
