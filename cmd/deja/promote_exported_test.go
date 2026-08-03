package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Taking a decision back is the same moment forget is: the machines that
// already have this session keep the note as it was. forget has said so since
// #788; promote said nothing (#898).
func TestPromoteSaysWhenTheSessionAlreadyLeft(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the winch brake pads squeal"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"w1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "w1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")

	// Nothing has left yet: a retraction says nothing about elsewhere.
	out, err := captureRun(t, "promote", "w1", "--state", "rejected", "--note", "did not hold")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "already sent") {
		t.Errorf("a session that never left was called sent:\n%s", out)
	}

	if _, err := index.ExportFull(dir, filepath.Join(tmp, "export")); err != nil {
		t.Fatal(err)
	}

	out, err = captureRun(t, "promote", "w1", "--state", "superseded", "--note", "replaced")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already sent") || !strings.Contains(out, "still reads as it did") {
		t.Errorf("a retraction after an export said nothing about the copies:\n%s", out)
	}

	// Accepting is not a retraction: no line.
	out, err = captureRun(t, "promote", "w1", "--state", "accepted", "--note", "back on")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "already sent") {
		t.Errorf("accepting was treated as a retraction:\n%s", out)
	}
}
