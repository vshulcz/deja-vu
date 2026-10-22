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

// Kimi Code 2.x /fork copies a session into a new id, the digest that opened
// the source among what it copies; the fork's first prompt put the same digest
// in front of the model a second time (#5146).
func TestKimiForkGetsNoSecondDigest(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "forkterm", []string{
		`{"type":"user","sessionId":"forkterm","timestamp":"` + old +
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
	kimi := filepath.Join(t.TempDir(), "kimi-code")
	t.Setenv("KIMI_CODE_HOME", kimi)
	t.Setenv("DEJA_KIMI_ROOT", "")
	state := func(id, forkedFrom string) {
		t.Helper()
		d := filepath.Join(kimi, "sessions", "wd_beta_1a2b", id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		st := map[string]any{"id": id, "version": 2, "cwd": cwd, "createdAt": 1782295200000}
		if forkedFrom != "" {
			st["forkedFrom"] = forkedFrom
		}
		b := mustMarshal(t, st)
		if err := os.WriteFile(filepath.Join(d, "state.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state("session_src", "")
	state("session_fork", "session_src")
	state("session_orphan", "session_never_asked")

	ask := func(sid string) string {
		t.Helper()
		b, err := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": sid, "cwd": cwd, "prompt": "anything"})
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(b))
		return captureStdout(t, func() { _ = runHookContextMode(index.DefaultDir(), true, true) })
	}
	if first := ask("session_src"); strings.TrimSpace(first) == "" {
		t.Fatal("the source opened with nothing, so there is no repeat to test")
	}
	if fork := ask("session_fork"); strings.TrimSpace(fork) != "" {
		t.Errorf("the fork got the digest its source already carries:\n%q", fork)
	}
	// A fork whose source deja never answered has no digest to carry.
	if orphan := ask("session_orphan"); strings.TrimSpace(orphan) == "" {
		t.Error("a fork of a session that never had a digest got none")
	}
}
