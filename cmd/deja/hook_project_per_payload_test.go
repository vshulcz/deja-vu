package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The payload says which project a hook call is about. deja used to write that
// answer into its own environment and read it back, and the write refused to
// overwrite what was there, so a second payload in the same process was
// answered with the first one's project — framed and injected exactly as a
// right answer would be (#2182; the write itself went in #2185).
//
// One process per invocation is the shape today, which is what made this a
// landmine rather than a fault.
func TestEachHookPayloadIsAnsweredForItsOwnProject(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "alpha", "one.jsonl"), "alpha1", []string{
		`{"type":"user","sessionId":"alpha1","timestamp":"` + old +
			`","message":{"role":"user","content":"the alpha work: pgbouncer runs in transaction mode"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "beta1", []string{
		`{"type":"user","sessionId":"beta1","timestamp":"` + old +
			`","message":{"role":"user","content":"the beta work: the kafka consumer keeps rebalancing"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	alpha := filepath.Join(base, "tmp", "alpha")
	beta := filepath.Join(base, "tmp", "beta")
	for _, d := range []string{alpha, beta} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Neither the working directory nor the environment names a project, so
	// the payload is the only thing that does.
	t.Chdir(base)
	t.Setenv("CLAUDE_PROJECT_DIR", "")

	answer := func(cwd string) string {
		t.Helper()
		withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "s", "cwd": cwd}))
		return captureStdout(t, func() { runHookContextPlain(t) })
	}
	// The premise: asked about alpha first, it answers about alpha.
	first := answer(alpha)
	if !strings.Contains(first, "transaction mode") {
		t.Fatalf("the first call did not carry alpha's memory, so this measures nothing:\n%s", first)
	}
	// The one that mattered: a second session, another project, same process.
	second := answer(beta)
	if strings.Contains(second, "transaction mode") {
		t.Errorf("a session in beta was handed alpha's memory:\n%s", second)
	}
	if !strings.Contains(second, "rebalancing") {
		t.Errorf("a session in beta was not handed beta's memory:\n%s", second)
	}
	// And back, so the answer follows the payload rather than latching onto
	// whichever project came last.
	third := answer(alpha)
	if !strings.Contains(third, "transaction mode") || strings.Contains(third, "rebalancing") {
		t.Errorf("the third call, about alpha again, was not answered about alpha:\n%s", third)
	}
}

// The doors are many and the fault was one line each: read the project out of
// the process environment rather than out of the payload that named it. This
// keeps that line from coming back anywhere in the hook files — the two places
// that may touch the export are the helper that reads the chain and the one
// that sets it, plus antigravity's guard on whether to set it at all.
func TestNoHookDoorReadsTheProjectOutOfTheEnvironment(t *testing.T) {
	files, err := filepath.Glob("hook_*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("found %d files, so this measures nothing", len(files))
	}
	allowed := map[string]bool{
		// hookCWD is the chain itself: payload, then whatever the host
		// exported, then where the process stands. It is the only place that
		// may read it.
		"hook_context.go": true,
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[filepath.Base(f)] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `os.Getenv("CLAUDE_PROJECT_DIR")`) {
			t.Errorf("%s reads the project out of the environment: the payload names it, and the export "+
				"is written once per process and will not change (#2182)", f)
		}
	}
}
