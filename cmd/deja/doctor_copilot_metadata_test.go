package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Copilot CLI keeps vscode.metadata.json beside each session's events.jsonl;
// it is the IDE's bookkeeping, not a transcript, and doctor reported every
// session as a file it could not read (#3303).
func TestDoctorDoesNotCallCopilotsMetadataUnrecognised(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "copilot", "session-state", "7dad4ddc-5bc8-4ded-bead-2bcad486028d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(`{"type":"user.message","data":{"content":"why does the retry loop drop the last attempt"},"timestamp":"2026-08-02T10:00:00.000Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vscode.metadata.json"), []byte(`{"workspaceFolder":"/w/api"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_COPILOT_ROOT", filepath.Join(tmp, "copilot", "session-state"))
	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "copilot") && strings.Contains(line, "not recognised") {
			t.Errorf("the IDE's metadata is counted as a transcript deja failed to read: %q", line)
		}
	}
}
