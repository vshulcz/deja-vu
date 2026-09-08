package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A store with more than one root can be half-readable: the CLI transcripts
// open and the VS Code tree refuses. The walk below says so — "sessions are
// missing from recall rather than the whole harness" (#816) — and the stat
// loop above it returned before setting the field, so the machine form called
// a half-readable store wholly denied (#3407).
func TestDoctorJSONSaysPartialWhenOnlyOneRootIsLocked(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	tmp := hermeticEnv(t)
	sessions := filepath.Join(tmp, "cline", "data", "sessions", "1757000001_aa11b")
	legacy := filepath.Join(tmp, "vscode", "globalStorage", "saoudrizwan.claude-dev")
	task := filepath.Join(legacy, "tasks", "1757000000000")
	for _, d := range []string{sessions, task} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLINE_ROOT", filepath.Dir(sessions))
	t.Setenv("DEJA_CLINE_ROOTS", legacy)
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(sessions, "1757000001_aa11b.messages.json"),
		`[{"role":"user","content":[{"type":"text","text":"why does the parser skip the first turn"}]}]`)
	write(filepath.Join(task, "api_conversation_history.json"),
		`[{"role":"user","content":[{"type":"text","text":"why does the retry loop drop the last attempt"}]}]`)

	locked := filepath.Join(legacy, "tasks")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot lock a directory here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if f, err := os.Open(locked); err == nil {
		_ = f.Close()
		t.Skip("this filesystem ignores the mode")
	}

	store, _ := inspectDoctorStore(doctorCheckNamed(t, "cline"))
	if store.State != "denied" {
		t.Fatalf("state = %q, want denied", store.State)
	}
	if store.Files == 0 {
		t.Fatalf("no file was read at all, so this asserts nothing")
	}
	if !store.Partial {
		t.Errorf("the CLI store read %d file(s) and the report calls the harness wholly denied", store.Files)
	}
}
