package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The note says the policy hides matching sessions, so a hidden session has to
// hold something friction would have reported before it counts. It counted
// every session that recorded any tool output at all, so a machine whose hidden
// history is `ok 12 tests passed` was told a rule was keeping its recurring
// failures back (#2794, the sibling of #2766).
func TestFrictionCountsTheWithheldSessionsItWouldHaveReported(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	quiet := `{"type":"user","timestamp":"2026-07-10T10:00:00Z","sessionId":"bbbb0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"a1","content":"ok 12 tests passed\nHEAD is now at 1a2b3c4"}]}}`
	if err := os.WriteFile(filepath.Join(store, "quiet.jsonl"), []byte(quiet+"\n"), 0o644); err != nil {
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
	if strings.Contains(note, "hides") {
		t.Errorf("the hidden session holds nothing friction reports, and the note claims otherwise:\n%s", note)
	}
	// The line on stdout counts something else — the sessions a rule took away
	// that recorded tool output at all — and it still has to be true, and still
	// has to name the rule that actually applies.
	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the trust policy withheld the 1 session that recorded tool output") {
		t.Errorf("the line about what the rule took away is wrong:\n%s", out)
	}

	// And a hidden session that does hold a failure is still counted: the
	// sentence has to keep working where it is true.
	loud := `{"type":"user","timestamp":"2026-07-10T11:00:00Z","sessionId":"cccc0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"a2","content":"connection refused on port 5432"}]}}`
	if err := os.WriteFile(filepath.Join(store, "loud.jsonl"), []byte(loud+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	note, err = captureRunStderr(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "hides 1 matching session") {
		t.Errorf("the session with a failure in it was not counted:\n%s", note)
	}
	out, err = captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "withheld the 2 sessions that recorded tool output") {
		t.Errorf("both sessions recorded tool output; the line says otherwise:\n%s", out)
	}
}
