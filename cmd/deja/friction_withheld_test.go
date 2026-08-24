package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The withheld count is a sentence about the trust policy — how much of their
// own history a rule keeps from the reader — and friction counted records where
// the sentence says sessions, so one hidden session with ten error lines was
// reported as ten (#1639).
func TestFrictionCountsWithheldSessionsNotRecords(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, `{"type":"user","timestamp":"2026-07-10T10:0`+string(rune('0'+i))+`:00Z","sessionId":"bbbb0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"a`+string(rune('0'+i))+`","content":"connection refused on port 5432"}]}}`)
	}
	if err := os.WriteFile(filepath.Join(store, "bbbb0001.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	note, err := captureRunStderr(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "hides 1 matching session") {
		t.Errorf("the store holds one session; the note says otherwise:\n%s", note)
	}
	if strings.Contains(note, "hides 10") {
		t.Errorf("record count reported as sessions:\n%s", note)
	}
}
