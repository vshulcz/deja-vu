package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// handoff with no id picks a session from this directory's project. It walked
// the same bare directory name #2333 fixed for the session start, through
// RecentProject, and packaged a client's acme/api for another agent (#2336).
func TestHandoffPicksFromTheCheckoutsOwnProject(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write := func(dirName, id, cwd, text string, hoursAgo int) {
		dir := filepath.Join(root, dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,`+
			`"message":{"role":"user","content":%q}}`, id, at, cwd, text)
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("-work-api", "mine", "/work/api", "my own retry budget question", 9)
	write("-clients-acme-api", "acme", "/clients/acme/api", "the acme ledger cutover", 1)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Run from a directory whose basename is the ambiguous one.
	work := filepath.Join(tmp, "work", "api")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	out, err := captureRun(t, "handoff")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "acme") {
		t.Errorf("handoff packaged another project's session:\n%s", out)
	}
	if !strings.Contains(out, "retry budget") {
		t.Errorf("handoff did not pick this project's own session:\n%s", out)
	}
}
