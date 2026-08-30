package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A pasted log is one message, and the index stores the first 64 KB of it.
// `deja show` printed that much and said nothing, so a reader looking for the
// line that explains a failure searched a log they believed was whole — the
// "showed part, read as whole" family, on the door where the whole point is to
// read a session as it was (#2467).
func TestShowSaysWhenAMessageWasClipped(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	// Longer than what the index keeps for one message.
	huge := strings.Repeat("2026-08-28T10:00:00Z INFO orders.pipeline step finished with pool=8 retries=0\n", 1800)
	line := `{"type":"user","sessionId":"huge","timestamp":"` + at + `","cwd":"/work/app",` +
		`"message":{"role":"user","content":` + jsonString(huge) + `}}`
	if err := os.WriteFile(filepath.Join(store, "huge.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, errOut := captureBoth(t, "show", "huge")
	whole := out + errOut
	if len(out) >= len(huge) {
		t.Fatalf("the fixture was not clipped at all: %d bytes shown", len(out))
	}
	if !strings.Contains(whole, "stored short of what the transcript holds") {
		t.Errorf("show printed part of a message and said nothing:\n%.400s", tail(whole))
	}
}

// tail is the end of the output, where a note about the message would sit.
func tail(s string) string {
	if len(s) <= 400 {
		return s
	}
	return s[len(s)-400:]
}
