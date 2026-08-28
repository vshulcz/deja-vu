package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A hook call must leave the process as it found it. Writing the payload's
// project into the environment carried one call's project into the next —
// which is #2182, and which made an earlier measurement of #2161 report the
// opposite of the truth, because a good call had left the export standing for
// the broken ones after it (#2185).
//
// The chain is untouched: payload, then whatever the host exported, then where
// the process stands. What is gone is deja writing to its own environment.
func TestAHookCallLeavesTheEnvironmentAsItFoundIt(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "beta1", []string{
		`{"type":"user","sessionId":"beta1","timestamp":"` + at +
			`","message":{"role":"user","content":"the pool timed out during the migration"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	project := filepath.Join(base, "tmp", "beta")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(base)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	payload := hookPayload(t, map[string]string{
		"source": "startup", "session_id": "s", "cwd": project,
		"prompt": "the pool timed out", "tool_name": "Bash",
	})

	// The premise: this store is one the doors can answer from, so a door that
	// says nothing is saying something.
	withHookStdin(t, payload)
	if out := captureStdout(t, func() { runHookContextPlain(t) }); !strings.Contains(out, "pool timed out") {
		t.Fatalf("the session-start door injected nothing, so an untouched environment measures nothing:\n%q", out)
	}

	for _, call := range []struct {
		name string
		run  func()
	}{
		{"session start", func() {
			withHookStdin(t, payload)
			_ = captureStdout(t, func() { runHookContextPlain(t) })
		}},
		{"prompt", func() {
			withHookStdin(t, payload)
			_, _ = captureRun(t, "hook-prompt")
		}},
		{"tool", func() {
			withHookStdin(t, payload)
			_, _ = captureRun(t, "hook-tool")
		}},
		{"antigravity", func() {
			withHookStdin(t, antigravityPayload(t, project))
			_, _ = captureRun(t, "hook-antigravity")
		}},
	} {
		t.Setenv("CLAUDE_PROJECT_DIR", "")
		call.run()
		if got := os.Getenv("CLAUDE_PROJECT_DIR"); got != "" {
			t.Errorf("the %s door left %q in the environment: the next call in this process would inherit it",
				call.name, got)
		}
	}
}

// The chain itself, which is what the export was standing in for: the payload
// first, the host's own export next, and where the process stands last.
func TestTheProjectChainPrefersThePayloadThenTheHost(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	if got := hookCWD("/w/from-payload"); got != "/w/from-payload" {
		t.Errorf("with nothing exported, the payload should decide: %q", got)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/w/from-host")
	if got := hookCWD("/w/from-payload"); got != "/w/from-payload" {
		t.Errorf("the payload names the project for this call, not the host's export: %q", got)
	}
	if got := hookCWD(""); got != "/w/from-host" {
		t.Errorf("with no cwd in the payload, the host's export decides: %q", got)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := hookCWD(""); got != wd {
		t.Errorf("with neither, where the process stands decides: %q, want %q", got, wd)
	}
	if strings.TrimSpace(wd) == "" {
		t.Fatal("the working directory is empty, so the last case measures nothing")
	}
}

// antigravityPayload marshals the workspace rather than pasting it into a JSON
// string: a Windows path is full of backslashes, and the decoder reads those as
// escapes.
func antigravityPayload(t *testing.T, workspace string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"workspacePaths": []string{workspace}, "invocationNum": 1})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
