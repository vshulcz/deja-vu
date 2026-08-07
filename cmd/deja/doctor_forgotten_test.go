package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A files-to-sessions gap has three causes; doctor explained a parse failure
// (#861) and an id collision (#1101) and left the reader's own forget
// unexplained — on the screen someone opens to check the forget took (#1108).
func TestDoctorSaysWhatWasForgotten(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a decision about the queue"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var before bytes.Buffer
	if err := runDoctor(&before, []string{"--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before.String(), "forgotten") {
		t.Errorf("a machine with nothing forgotten mentions it:\n%s", before.String())
	}

	if _, err := captureRun(t, "forget", "--session", "claude:s1"); err != nil {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := runDoctor(&after, []string{"--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after.String(), "1 session forgotten here") {
		t.Errorf("doctor does not say the index is empty because of a forget:\n%s", after.String())
	}
}
