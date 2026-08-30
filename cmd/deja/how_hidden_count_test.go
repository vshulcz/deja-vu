package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// howEntries counted withheld records and the sentence it feeds says sessions,
// so one hidden session that ran a command five times was reported as five
// (#1641, the shape #1639 fixed for friction). The CLI and the MCP how tool
// both pass that number on.
func TestHowCountsWithheldSessionsNotRecords(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 5; i++ {
		lines = append(lines, `{"type":"assistant","timestamp":"2026-07-10T10:0`+string(rune('0'+i))+`:00Z","sessionId":"bbbb0002-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`)
	}
	if err := os.WriteFile(filepath.Join(store, "bbbb0002.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
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

	out, err := captureRun(t, "how", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hides 1 matching session") {
		t.Errorf("the store holds one session; the note says otherwise:\n%s", out)
	}
	if strings.Contains(out, "hides 5") {
		t.Errorf("record count reported as sessions:\n%s", out)
	}
}
