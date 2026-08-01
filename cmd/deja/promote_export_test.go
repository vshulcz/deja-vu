package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `--to` exists to hand a decision to someone, and it was the one outbound
// path that neither ran the redaction pass nor printed the floor `share` and
// `sync export` both end with (#848).
func TestPromoteToRedactsAndSaysSo(t *testing.T) {
	hermeticEnv(t)
	notesDir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(notesDir, "notes.jsonl"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the staging credentials live in the vault"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"w3","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "decision.md")
	secret := "ghp_abcdefghij0123456789abcdefghij0123"
	msg, err := captureRunStderr(t, "promote", "w3", "--state", "accepted",
		"--note", "vault path acme/staging, token "+secret, "--to", out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "pattern redaction is a floor") {
		t.Errorf("the file handed to someone was written without the floor warning:\n%s", msg)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) {
		t.Errorf("the token was written verbatim:\n%s", body)
	}
	if !strings.Contains(string(body), "vault path acme/staging") {
		t.Errorf("the writer's own words were lost:\n%s", body)
	}
	if !strings.Contains(string(body), "state: accepted") {
		t.Errorf("the note lost its state:\n%s", body)
	}
}
