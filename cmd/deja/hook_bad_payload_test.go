package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// A hook fed something that is not a payload carries on with the zero value.
// Two halves of that are worth holding apart, and neither was held: the memory
// still reaches the agent — an agent that asked for context gets it, whatever
// its host wrote to stdin — and the receiver the payload was carrying is lost,
// which leaves the injection log unable to answer the question #1949 gave it
// the `into` field for (#2161).
func TestAHookPayloadItCannotReadStillInjects(t *testing.T) {
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
	// Neither the environment nor the working directory names the project, so
	// what the hook knows is what the payload told it — the case #759 is about,
	// and the only shape where the payload's own contribution is visible.
	elsewhere := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := index.DefaultDir()

	// The premise: a payload deja can read injects the memory and records where
	// it went.
	withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "ses1", "cwd": cwd}))
	out := captureStdout(t, func() { runHookContextPlain(t) })
	if !strings.Contains(out, "transaction mode") {
		t.Fatalf("the hook injected nothing on a good payload, so this measures nothing:\n%q", out)
	}
	if into := latestSnapshotInto(t, dir); into != "ses1" {
		t.Fatalf("a good payload recorded the receiver as %q", into)
	}

	for _, tc := range []struct{ name, payload string }{
		{"prose", "this is not json at all"},
		{"truncated", `{"session_id":`},
		{"binary", "\x00\x01binary"},
		{"empty", ""},
	} {
		before := len(usage.Snapshots(dir, 0))
		withHookStdin(t, tc.payload)
		out := captureStdout(t, func() { runHookContextPlain(t) })
		if !strings.Contains(out, "transaction mode") {
			t.Errorf("%s: the agent asked for context and got none:\n%q", tc.name, out)
		}
		// The row this call wrote, not whatever the last one left behind.
		if after := len(usage.Snapshots(dir, 0)); after != before+1 {
			t.Fatalf("%s: the injection log grew by %d rows, so the receiver below is another call's", tc.name, after-before)
		}
		// The receiver is what a broken payload costs today. #2161 asks whether
		// it should stay that way; when that is decided, this line is the one
		// to change.
		if into := latestSnapshotInto(t, dir); into != "" {
			t.Errorf("%s: the log names %q as the receiver of an injection whose payload deja could not read", tc.name, into)
		}
	}
}

// latestSnapshotInto is who the newest recorded injection went to.
func latestSnapshotInto(t *testing.T, dir string) string {
	t.Helper()
	snaps := usage.Snapshots(dir, 1)
	if len(snaps) == 0 {
		t.Fatal("nothing was recorded in the injection log")
	}
	return snaps[0].Into
}
