package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A session whose timestamp was missing or unparseable carries the Go zero
// time, and "0001-01-01" reads as corrupted data rather than as a missing
// field (#765).
func TestLastPrintsADashForAMissingTimestamp(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(name, line string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notime.jsonl", `{"type":"user","sessionId":"notime","cwd":"/w/p","message":{"role":"user","content":"a session with no timestamp field at all"}}`)
	write("ok.jsonl", `{"type":"user","sessionId":"ok","cwd":"/w/p","timestamp":"2026-07-30T10:00:00Z","message":{"role":"user","content":"an ordinary session"}}`)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "last", "5")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "0001-01-01") {
		t.Errorf("printed the zero date:\n%s", out)
	}
	if !strings.Contains(out, "· - · notime") {
		t.Errorf("no dash for the undated session:\n%s", out)
	}
	// The dated one still shows its date.
	if !strings.Contains(out, "2026-07-30 · ok") {
		t.Errorf("dated session lost its date:\n%s", out)
	}
	// JSON keeps the raw value: a machine reader wants the field as stored.
	jsonOut, err := captureRun(t, "last", "5", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut, "0001-01-01T00:00:00Z") {
		t.Errorf("json lost the zero time:\n%s", jsonOut)
	}
}
