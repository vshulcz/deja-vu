package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The session-start hook served 606 injections over six weeks and recorded not
// one of the sessions inside them, so its repetition could not be counted at
// all — while the per-prompt path, which does record ids, turned out to be
// re-serving 74 sessions at a 92% repeat rate (#2038). A surface nobody can
// measure is a surface nobody fixes.
func TestTheSessionStartHookLogsWhatItServed(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "idsterm", []string{
		`{"type":"user","sessionId":"idsterm","timestamp":"` + old +
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

	withHookStdin(t, `{"source":"startup","session_id":"ses_ids","cwd":"`+cwd+`"}`)
	if out := captureStdout(t, func() {
		if err := runHookContext(index.DefaultDir(), true); err != nil {
			t.Error(err)
		}
	}); !strings.Contains(out, "transaction mode") {
		t.Fatalf("the hook injected nothing, so there is nothing to log:\n%q", out)
	}

	ids := loggedIDsForKind(t, index.DefaultDir(), "hook")
	if len(ids) == 0 {
		t.Fatal("the session-start hook logged an injection with no session ids")
	}
	if !ids["idsterm"] {
		t.Errorf("the log names %v, not the session that was served", ids)
	}
}

// A cache hit has to log the same thing a fresh build does: the digest is
// cached per project and most session starts are hits, so ids that only
// survived the cold path would leave the common case unmeasured.
func TestACachedDigestStillLogsItsSessions(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "cacheterm", []string{
		`{"type":"user","sessionId":"cacheterm","timestamp":"` + old +
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

	// First call fills the cache; the second must be the hit.
	if _, _, _, _, _, ids, _ := cachedHookDigest(index.DefaultDir()); len(ids) == 0 {
		t.Fatal("the cold path carried no ids, so the cached one cannot be judged")
	}
	_, _, _, _, _, cached, _ := cachedHookDigest(index.DefaultDir())
	if len(cached) == 0 {
		t.Error("a cache hit lost the sessions the digest carries")
	}
}
