package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A dropped flag fell back to the incremental path, which on a second run has
// nothing left to send: `--ful` exported zero records into an empty directory
// while the reader believed they had carried their whole memory across (#745).
func TestSyncExportRejectsUnknownFlags(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry budget"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, flag := range []string{"--ful", "--fully", "-full", "--json"} {
		_, err := captureRun(t, "sync", "export", filepath.Join(tmp, "out-"+flag), flag)
		if err == nil {
			t.Errorf("%s was accepted", flag)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: %v", flag, err)
		}
	}

	// The real flag still works, and so does a plain export.
	if _, err := captureRun(t, "sync", "export", filepath.Join(tmp, "ok-full"), "--full"); err != nil {
		t.Fatalf("--full: %v", err)
	}
	if _, err := captureRun(t, "sync", "export", filepath.Join(tmp, "ok-plain")); err != nil {
		t.Fatalf("plain: %v", err)
	}
	// A stray argument to import is a mistake too — it takes one directory.
	if _, err := captureRun(t, "sync", "import", filepath.Join(tmp, "ok-full"), "extra"); err == nil ||
		!strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("import with extra arg: %v", err)
	}
	if _, err := captureRun(t, "sync", "import", filepath.Join(tmp, "ok-full")); err != nil {
		t.Errorf("import: %v", err)
	}
}
