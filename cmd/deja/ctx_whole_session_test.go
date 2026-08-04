package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// ctx is the command the hook tells an agent to call. It printed the hit's
// snippets rather than the session, and a correction rarely repeats the words
// of the decision it reverses — so the agent was handed a decision that had
// been withdrawn (#1011).
func TestCtxCarriesTheWholeDecisionChain(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"we raised the pool cap to 200"},"timestamp":"2026-08-04T10:00:00Z","sessionId":"w25","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "w25", "--state", "accepted", "--note", "the pool cap is 200"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "w25", "--state", "rejected", "--note", "rolled back, the cap stays at 20"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "ctx", "pool cap")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rolled back") {
		t.Errorf("ctx dropped the correction that reverses the decision:\n%s", out)
	}
	if !strings.Contains(out, "the pool cap is 200") {
		t.Errorf("ctx dropped the decision being corrected:\n%s", out)
	}
	if strings.Index(out, "rolled back") > strings.Index(out, "the pool cap is 200") {
		t.Errorf("the withdrawal is below the decision it takes back:\n%s", out)
	}

	// An ordinary transcript still answers with what the query asked about.
	out, err = captureRun(t, "ctx", "raised the pool")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "we raised the pool cap to 200") {
		t.Errorf("ctx on a transcript lost the matching turn:\n%s", out)
	}
}
