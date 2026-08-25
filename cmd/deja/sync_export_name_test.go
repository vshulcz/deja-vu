package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The CLI wrapper has its own family of sentences naming the export directory,
// and they printed the path raw while the index package's five did not (#1857).
func TestTheExportSentencesAreSafeToPrint(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"v1","cwd":"/proj","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"the retry budget question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// A parent that cannot be created because a file sits where it would go:
	// MkdirAll fails, and the sentence names the path.
	blocker := filepath.Join(tmp, "gone\x1b[31mHACK\rrewound")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Skipf("the filesystem refused the name: %v", err)
	}
	odd := filepath.Join(blocker, "batch")
	err := runSync(dir, []string{"export", odd})
	if err == nil {
		t.Fatal("exporting into a missing parent was accepted")
	}
	if strings.ContainsAny(err.Error(), "\x1b\r") {
		t.Errorf("the sentence carries an escape or a rewind: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "batch") {
		t.Errorf("the sentence no longer names the directory: %q", err.Error())
	}
}
