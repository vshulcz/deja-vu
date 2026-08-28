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

// A hook fed something that is not a payload carries on with the zero value,
// and what that costs depends on what else names the project. On a host that
// exports nothing and runs the hook outside the project — the shape #759 is
// about — the call stands nowhere: no project, no memory, no row. On a host
// that does export it, the memory still goes out and the log records an
// injection whose receiver is unknown, which is the half #2161 asks about.
// Both are here, because a fix for either has to keep the other in view.
//
// The first version of this test ran the broken payloads after a good one in
// the same process, where `adoptHookCWD`'s export was still standing from the
// good call — so it measured a leak rather than the product and concluded that
// the memory still goes out. Each case starts here where a fresh process
// starts: nothing adopted.
func TestAPayloadDejaCannotReadLosesTheProjectItNamed(t *testing.T) {
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
		// Where a fresh process starts: the export the good call above left is
		// not something another invocation would have.
		t.Setenv("CLAUDE_PROJECT_DIR", "")
		before := len(usage.Snapshots(dir, 0))
		withHookStdin(t, tc.payload)
		out := captureStdout(t, func() { runHookContextPlain(t) })
		if strings.Contains(out, "transaction mode") {
			t.Errorf("%s: a payload deja could not read was answered with a project's memory anyway:\n%q", tc.name, out)
		}
		if strings.TrimSpace(out) != "" {
			t.Errorf("%s: the hook said something on a payload it could not read:\n%q", tc.name, out)
		}
		// And nothing is recorded, so the log does not carry an injection that
		// never happened — which is the other half of what #2161 asks: an
		// unreadable payload leaves no trace at all, of the call or of the
		// silence.
		if after := len(usage.Snapshots(dir, 0)); after != before {
			t.Errorf("%s: the injection log grew by %d rows for a call that injected nothing", tc.name, after-before)
		}
	}

	// The other host: it exports the project, so a payload deja cannot read
	// still knows where it is standing. The memory goes out and the log keeps
	// a row — with no receiver, because that is what the payload was carrying.
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	before := len(usage.Snapshots(dir, 0))
	withHookStdin(t, "this is not json at all")
	out = captureStdout(t, func() { runHookContextPlain(t) })
	if !strings.Contains(out, "transaction mode") {
		t.Errorf("a host that names the project got no memory from a broken payload:\n%q", out)
	}
	if after := len(usage.Snapshots(dir, 0)); after != before+1 {
		t.Fatalf("the injection log grew by %d rows, so the receiver below is another call's", after-before)
	}
	// #2161 asks whether this should stay empty; when that is decided, this is
	// the line to change.
	if into := latestSnapshotInto(t, dir); into != "" {
		t.Errorf("the log names %q as the receiver of an injection whose payload deja could not read", into)
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
