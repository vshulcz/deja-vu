package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An empty store is not a miss on the argument: deja has nothing to look in,
// and "no sessions mention it" reads as "looked, not there" (#834).
func TestEmptyStoreAnswersPointAtTheStores(t *testing.T) {
	hermeticEnv(t)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"files", []string{"files", "anything"}},
		{"restore", []string{"restore", "some/file.go"}},
		{"ctx", []string{"ctx", "anything"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := captureRun(t, tc.args...)
			msg := out
			if err != nil {
				msg += err.Error()
			}
			if !strings.Contains(msg, "no agent history was found on this machine") {
				t.Errorf("empty store answered as a miss on the argument:\n%s", msg)
			}
		})
	}

	// With history, the ordinary answers stay: the store was searched and the
	// thing genuinely is not in it.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the hydraulic pump bearing failed"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"a1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"files", []string{"files", "zzzqqq"}},
		{"restore", []string{"restore", "some/file.go"}},
		{"ctx", []string{"ctx", "zzzqqqwww"}},
	} {
		t.Run(tc.name+" with history", func(t *testing.T) {
			out, err := captureRun(t, tc.args...)
			msg := out
			if err != nil {
				msg += err.Error()
			}
			if strings.Contains(msg, "no agent history") {
				t.Errorf("a store with history was called empty:\n%s", msg)
			}
		})
	}
}
