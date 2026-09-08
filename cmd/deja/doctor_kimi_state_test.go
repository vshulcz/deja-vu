package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kimi keeps state.json beside each session's agents/main/wire.jsonl and the
// reader opens it for the title and the working directory; doctor counted one
// per session as a file it could not read (#3309).
func TestDoctorDoesNotCallKimisStateUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "kimi-code")
	dir := filepath.Join(root, "sessions", "wd_proj_abc", "session_1")
	if err := os.MkdirAll(filepath.Join(dir, "agents", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "main", "wire.jsonl"),
		[]byte(`{"role":"user","content":"why does the retry loop drop the last attempt","origin":{"kind":"user"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"title":"retry loop","workDir":"/w/api"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", root)
	t.Setenv("DEJA_KIMI_ROOT", root)
	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "kimi") && strings.Contains(line, "not recognised") {
			t.Errorf("the session's own state file is counted as a transcript deja failed to read: %q", line)
		}
	}
}
