package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The index lived on a disk that went away between sessions. `deja index` and
// doctor call that unmounted; the hook called it a permission problem and sent
// the reader to check a directory that is not there (#1054).
func TestHookNamesAnUnmountedIndexVolume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root creates directories under a read-only parent anyway")
	}
	tmp := hermeticEnv(t)
	// The shape of an external volume: a mount point under a directory this
	// user cannot write, so the path cannot simply be recreated.
	volumes := filepath.Join(tmp, "Volumes")
	dir := filepath.Join(volumes, "ext", "deja", "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", dir)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"zeppelin gasket"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(filepath.Join(volumes, "ext")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(volumes, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(volumes, 0o700) })

	message := func() string {
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) == "" {
			return ""
		}
		var resp struct {
			SystemMessage string `json:"systemMessage"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q", out)
		}
		return resp.SystemMessage
	}

	got := message()
	if !strings.Contains(got, "unmounted") {
		t.Errorf("hook did not say the disk is gone: %q", got)
	}
	if strings.Contains(got, "not writable") {
		t.Errorf("hook blamed permissions on a directory that is not there: %q", got)
	}

	// A directory that is there and read-only is a permission problem, and
	// keeps the wording that names one.
	if err := os.Chmod(volumes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStaleFormatIndex(t, dir)
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if got := message(); !strings.Contains(got, "not writable") {
		t.Errorf("a read-only index directory lost the permission wording: %q", got)
	}
}
