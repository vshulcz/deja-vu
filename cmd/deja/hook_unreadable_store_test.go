package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja index` and `deja doctor` both name a store deja could not read, and
// neither is what someone who installed deja and went back to their agent ever
// runs: the project simply had no memory of those sessions and nothing said
// why (#917).
func TestHookNamesAStoreItCouldNotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	open := filepath.Join(root, "-proj")
	if err := os.MkdirAll(open, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(open, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "b.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The hook takes the project from the environment when the payload does
	// not name one; this is the project whose sessions were readable.
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	message := func(t *testing.T) string {
		t.Helper()
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		// Nothing to say prints nothing at all, which is the second call here.
		if strings.TrimSpace(out) == "" {
			return ""
		}
		var resp struct {
			SystemMessage string `json:"systemMessage"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q (%v)", out, err)
		}
		return resp.SystemMessage
	}

	got := message(t)
	if !strings.Contains(got, "could not read") || !strings.Contains(got, "deja doctor") {
		t.Errorf("the hook said nothing about the locked store: %q", got)
	}
	// …and it is not repeated every session: the same standing fact is
	// wallpaper by the second time.
	if again := message(t); strings.Contains(again, "could not be read") {
		t.Errorf("the note repeated on the next session: %q", again)
	}
}
