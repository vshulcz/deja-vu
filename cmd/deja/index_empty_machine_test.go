package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `deja index` on a machine with no agent history printed the "indexing ..."
// line and nothing else: the step whose job is filling memory ended with no
// result at all, and the state only surfaced on the next command.
func TestIndexOnAMachineWithNoHistorySaysSo(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "idx")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	err = cmdIndex(dir, nil)
	os.Stderr = stderr
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "nothing to index yet") {
		t.Errorf("index on an empty machine said nothing about the empty store:\n%s", got)
	}
	if !strings.Contains(got, "no agent history was found on this machine") {
		t.Errorf("index did not name why the store is empty:\n%s", got)
	}
	if !strings.Contains(got, "deja sources") {
		t.Errorf("index gave no next step:\n%s", got)
	}
}

// The same line must not appear once there is history: a real build already
// prints its per-harness counts.
func TestIndexWithHistoryDoesNotClaimNothingToIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"pool exhausted"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	dir := filepath.Join(tmp, "idx")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	err = cmdIndex(dir, nil)
	os.Stderr = stderr
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); strings.Contains(got, "nothing to index yet") {
		t.Errorf("index called a store with a session empty:\n%s", got)
	}
}
