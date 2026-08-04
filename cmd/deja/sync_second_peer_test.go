package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The watermark is per machine, not per destination: the second peer someone
// hands memory to gets an empty folder and the same "exported 0 records" that
// means "you are up to date" at the first one (#982).
func TestExportToASecondDestinationSaysWhyItIsEmpty(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	first := filepath.Join(tmp, "to-laptop")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "sync", "export", first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exported 1 records") {
		t.Fatalf("the first export did not carry the session:\n%s", out)
	}
	// Same destination again is the up-to-date case and must stay quiet about
	// --full: there is nothing to send there.
	out, err = captureRun(t, "sync", "export", first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--full") {
		t.Errorf("a destination that already holds the batch was told to resend:\n%s", out)
	}

	second := filepath.Join(tmp, "to-desktop")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "sync", "export", second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--full") {
		t.Errorf("an empty second destination said nothing about how to fill it:\n%s", out)
	}
	entries, err := os.ReadDir(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the incremental export wrote something after all: %v", entries)
	}

	// And --full fills it, after which the folder is up to date like any other.
	if _, err := captureRun(t, "sync", "export", second, "--full"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "sync", "export", second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--full") {
		t.Errorf("a filled destination kept being told to resend:\n%s", out)
	}
}
