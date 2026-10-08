package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Grok needs summary.json beside updates.jsonl. One already there stops the
// write before anything lands, and the two go back together otherwise.
func TestWriteBackSidecarIsNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	id := "01a11b02-2491-7ae0-ba1b-48dd82cc8841"
	dir := filepath.Join(root, "sessions", "%2Fwork%2Fapi", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(dir, "summary.json")
	if err := os.WriteFile(summary, []byte(`{"mine":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s := model.Session{Harness: "grok", ID: id, Path: filepath.Join(dir, "updates.jsonl"), Started: at,
		Messages: []model.Message{{Role: "user", Text: "why", Time: at}, {Role: "assistant", Text: "because", Time: at}}}
	err := writeBackSession(t.TempDir(), s, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "summary.json") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Errorf("updates.jsonl was written beside a summary.json deja did not write")
	}
	if b, _ := os.ReadFile(summary); string(b) != `{"mine":true}` {
		t.Errorf("summary.json changed: %s", b)
	}

	if err := os.Remove(summary); err != nil {
		t.Fatal(err)
	}
	if err := writeBackSession(t.TempDir(), s, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{s.Path, summary} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s not written: %v", filepath.Base(p), err)
		}
	}
}
