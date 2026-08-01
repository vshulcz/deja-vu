package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An empty store is not a query problem: "fewer words" cannot help when
// nothing is indexed, and last, blame and the brief all say what to do
// instead. Search is the command a new machine reaches for first (#832).
func TestSearchOnAnEmptyStorePointsAtTheStores(t *testing.T) {
	hermeticEnv(t)

	out, err := captureRunStderr(t, "anything")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no agent history was found on this machine") {
		t.Errorf("an empty machine still gets query advice:\n%s", out)
	}
	if strings.Contains(out, "try fewer words") {
		t.Errorf("advice that cannot be followed is still printed:\n%s", out)
	}

	// With history indexed, an ordinary miss keeps the ordinary answer.
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
	miss, err := captureRunStderr(t, "zzzqqq")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(miss, "no matches in 1 indexed session") {
		t.Errorf("an ordinary miss lost its answer:\n%s", miss)
	}
	if strings.Contains(miss, "no agent history") {
		t.Errorf("a store with history was called empty:\n%s", miss)
	}
}
