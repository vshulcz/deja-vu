package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The listing whose job is "most recent" led with a session that has not
// happened yet and said nothing, while the first screen says it — which is the
// arrangement #696 rejected there (#2104).
func TestLastSaysWhenASessionIsStampedAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the pool was exhausted while the migration held the lock"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	ahead, err := json.Marshal(map[string]any{
		"ts": time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s9", "state": "accepted", "project": "app",
		"title": "a note from next year", "text": "pgbouncer runs in transaction mode",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, append(ahead, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "last") })
	// The premise: the future session really does lead the listing.
	first := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	if !strings.Contains(first, "next year") {
		t.Fatalf("the session stamped ahead is not at the top, so this measures nothing: %q", first)
	}
	if !strings.Contains(stderr, "later than this machine's clock") {
		t.Errorf("the listing led with work that has not happened and said nothing: %q", strings.TrimSpace(stderr))
	}
}

// And it says it only when there is one: the line is about a state, not a
// decoration on every listing.
func TestLastIsQuietWhenNothingIsAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the pool was exhausted"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "last") })
	if !strings.Contains(out, "pool was exhausted") {
		t.Fatalf("the listing is empty, so this measures nothing: %q", out)
	}
	if strings.Contains(stderr, "later than this machine's clock") {
		t.Errorf("a listing with nothing ahead said something was: %q", strings.TrimSpace(stderr))
	}
}
