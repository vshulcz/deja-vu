package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The session-start digest is cached as prose beside the index, one file per
// working directory, and nothing removed one — so after `deja forget` the
// block quoting the forgotten session was still on disk. Recall was not
// serving it (the entry expires in a minute), but the privacy command is about
// the content, not only the answer (#3411, the shape #2537 closed for a note).
func TestForgetTakesTheCachedDigestWithIt(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-w-api", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","cwd":"/w/api","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer overflows under load"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	cwd := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := hookCacheEntry{At: time.Now(), CWD: cwd, Gate: hookGate(), Digest: "recalled: the zonkobuffer overflows under load", Sessions: 1, Raw: 7}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	cache := hookCachePath(dir, cwd)
	if err := os.WriteFile(cache, b, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "forget", "--session", "s1"); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadFile(cache)
	if err == nil && strings.Contains(string(left), "zonkobuffer") {
		t.Errorf("the forgotten session is still quoted in %s", filepath.Base(cache))
	}
}
