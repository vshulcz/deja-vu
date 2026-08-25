package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A watermark is namespaced by the peer string an export was given, and one
// machine spelled two ways got two of them: the second export sent the same
// records again and reported them as work delivered (#1878). ssh has treated
// the two spellings as one machine since #1867.
func TestOneMachineSettlesUnderOneWatermarkWhicheverCase(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 4; i++ {
		lines = append(lines, `{"type":"user","sessionId":"s`+string(rune('a'+i))+`","cwd":"/proj","timestamp":"2026-08-20T10:0`+string(rune('0'+i))+`:00Z","message":{"role":"user","content":"retry loop"}}`)
	}
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	first, err := captureRun(t, "sync", "export", filepath.Join(tmp, "one"), "--peer", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "exported 4 records") {
		t.Fatalf("the first export did not send the history: %s", first)
	}
	again, err := captureRun(t, "sync", "export", filepath.Join(tmp, "two"), "--peer", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "exported 0 records") {
		t.Errorf("the same machine spelled differently was sent everything again: %s", again)
	}

	// The control: a different machine still receives the history, so the
	// watermarks are folded rather than shared.
	other, err := captureRun(t, "sync", "export", filepath.Join(tmp, "three"), "--peer", "build-box")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(other, "exported 4 records") {
		t.Errorf("a machine that has received nothing was told it was up to date: %s", other)
	}
}
