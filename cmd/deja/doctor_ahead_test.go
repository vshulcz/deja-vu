package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedAheadIndex(t *testing.T) {
	t.Helper()
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
}

// doctor is where someone looks when a store reads wrong, and it named this for
// a peer's clock since #1855 while saying nothing about a session's (#2106).
func TestDoctorNamesSessionsStampedAhead(t *testing.T) {
	seedAheadIndex(t)
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the index really holds the session ahead.
	if !strings.Contains(out, "freshness") {
		t.Fatalf("doctor did not report on the index at all: %q", out)
	}
	if !strings.Contains(out, "later than this machine's clock") {
		t.Errorf("doctor says nothing about a session dated next year:\n%s", out)
	}
}

// And the machine-readable side, which is what a script reads.
func TestDoctorJSONCarriesTheAheadCount(t *testing.T) {
	seedAheadIndex(t)
	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Index struct {
			SessionsAhead int `json:"sessions_stamped_ahead"`
		} `json:"index"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json: %v: %s", err, out)
	}
	if report.Index.SessionsAhead != 1 {
		t.Errorf("doctor --json counts %d sessions stamped ahead, want 1", report.Index.SessionsAhead)
	}
}

// A store with nothing ahead says nothing: the line is a state, not a
// decoration on every run.
func TestDoctorIsQuietWhenNothingIsAhead(t *testing.T) {
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
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "later than this machine's clock") {
		t.Errorf("doctor reported a session ahead where there is none:\n%s", out)
	}
}
