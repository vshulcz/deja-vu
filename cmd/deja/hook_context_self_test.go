package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// opencode asks for the session-start digest after the first message is
// stored, so the index can already hold the session asking — and the digest
// served it its own opening prompt as history (#4199). The payload names the
// session; the digest leaves it out, cached or not.
func TestSessionStartLeavesOutTheSessionAsking(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	now := time.Now()
	seedClaudeAt(t, claude, "app", "older-session", "the ledger export drops the last row", "the writer missed a final flush", now.Add(-2*time.Hour))
	seedClaudeAt(t, claude, "app", "live-session", "the glimmerquest cache misses on every cold start", "warming it at boot from the last snapshot fixed it", now.Add(-time.Minute))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	run := func(payload string) string {
		withHookStdin(t, payload)
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	// Another session in the project: the live one is history to it.
	if out := run(`{"session_id":"someone-else","cwd":"/tmp/app"}`); !strings.Contains(out, "glimmerquest") {
		t.Fatalf("the control did not recall the recent session:\n%s", out)
	}
	// The session itself, after that digest was cached for the project.
	out := run(`{"session_id":"live-session","cwd":"/tmp/app"}`)
	if strings.Contains(out, "glimmerquest") {
		t.Fatalf("the digest served the session asking its own prompt:\n%s", out)
	}
	if !strings.Contains(out, "ledger export") {
		t.Fatalf("leaving the session out took the rest of the digest with it:\n%s", out)
	}
	// The project cache is shared: the next session to open still sees it.
	if out := run(`{"session_id":"third","cwd":"/tmp/app"}`); !strings.Contains(out, "glimmerquest") {
		t.Fatalf("the digest cached without the asker hid it from the next session:\n%s", out)
	}
	// After a compaction the lead sends the agent to its own session's id.
	if out := run(`{"session_id":"live-session","source":"compact","cwd":"/tmp/app"}`); !strings.Contains(out, "glimmerquest") {
		t.Fatalf("a compacted session lost itself from the digest:\n%s", out)
	}
}

// Both plugin shapes name the session when they ask for the digest.
func TestOpencodePluginsNameTheSessionToTheDigest(t *testing.T) {
	for name, js := range map[string]string{
		"1.x": opencodeLegacyPluginJS("/usr/local/bin/deja"),
		"2.x": opencodePluginJS("/usr/local/bin/deja"),
	} {
		calls := 0
		for _, line := range strings.Split(js, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !strings.Contains(line, "hook-context") {
				continue
			}
			calls++
			if !strings.Contains(line, `session_id: input.sessionID || ""`) && !strings.Contains(line, `session_id: event.sessionID || ""`) {
				t.Errorf("%s plugin asks for the digest without the session id: %s", name, strings.TrimSpace(line))
			}
		}
		if calls == 0 {
			t.Errorf("%s plugin never asks for the digest", name)
		}
	}
}
