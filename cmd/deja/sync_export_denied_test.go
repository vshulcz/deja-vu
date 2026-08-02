package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The last write path still handing back a syscall: `open …/deja-sync-3e5cfa…
// .jsonl: permission denied` names a file deja was about to create, with a
// name nobody chose (#893).
func TestSyncExportIntoAnUnwritableDirectorySaysWhatToFix(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "export")
	if err := os.MkdirAll(out, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(out, 0o700) })

	_, err := captureRun(t, "sync", "export", "--full", out)
	if err == nil {
		t.Fatal("an export into an unwritable directory succeeded")
	}
	got := err.Error()
	if !strings.Contains(got, "cannot write the export into "+out) {
		t.Errorf("does not name the directory: %q", got)
	}
	if strings.Contains(got, "deja-sync-") {
		t.Errorf("names a file deja was about to create: %q", got)
	}

	// And the refusal left the watermarks alone: everything is still owed.
	if err := os.Chmod(out, 0o700); err != nil {
		t.Fatal(err)
	}
	n, err := index.Export(os.Getenv("DEJA_INDEX_DIR"), out)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("the failed export moved the watermark")
	}
}
