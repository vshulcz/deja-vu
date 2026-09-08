package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Continue keeps sessions.json beside the session documents as their list;
// the reader reads it for titles and dates and skips it as a transcript on
// purpose, and doctor counted it as "1 not recognised here" on every Continue
// store (#3297).
func TestDoctorDoesNotCallContinuesListUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "continue", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid := "8f1c2a3e-0000-4000-8000-000000000000"
	if err := os.WriteFile(filepath.Join(dir, sid+".json"), []byte(`{"sessionId":"`+sid+`","title":"retry loop","workspaceDirectory":"/w/api","history":[{"message":{"role":"user","content":"why does the retry loop drop the last attempt"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`[{"sessionId":"`+sid+`","title":"retry loop","dateCreated":"2026-08-02T10:00:00.000Z","workspaceDirectory":"/w/api"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CONTINUE_ROOT", filepath.Join(tmp, "continue"))
	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "continue") && strings.Contains(line, "not recognised") {
			t.Errorf("the list file is counted as a transcript deja failed to read: %q", line)
		}
	}
}
