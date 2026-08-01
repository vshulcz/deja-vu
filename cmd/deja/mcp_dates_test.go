package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every human surface dates a session in the reader's zone since #849. The
// surfaces an agent reads did not, so a model quoting a date back to the user
// named a different day from the one on their screen (#856).
func TestAgentSurfacesDateInTheReadersZone(t *testing.T) {
	// 22:00 UTC is the next day anywhere east of UTC+2.
	stamp := time.Date(2026, 7, 1, 22, 0, 0, 0, time.UTC)
	local := stamp.Local().Format("2006-01-02")
	utc := stamp.UTC().Format("2006-01-02")
	if local == utc {
		t.Skip("this machine's zone cannot tell the two apart")
	}

	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"late evening session about the winch brake"},"timestamp":"` +
		stamp.Format(time.RFC3339) + `","sessionId":"late1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	raw, code, msg := mcpResourcesList(indexDirForTest())
	if code != 0 {
		t.Fatalf("resources/list failed: %d %s", code, msg)
	}
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), local) {
		t.Errorf("resource listing does not carry the local date %s:\n%s", local, body)
	}
	if strings.Contains(string(body), utc) {
		t.Errorf("resource listing still carries the UTC date %s:\n%s", utc, body)
	}
}
