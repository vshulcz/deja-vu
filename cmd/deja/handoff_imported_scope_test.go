package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The scope rule keeps bare-name matching for synced projects on purpose, so a
// peer's goprojects/svc answers to a local svc. handoff took that as licence to
// package a teammate's clients/acme/api from a directory named api — content
// prepared for another agent, picked because it was the newest (#2347).
func TestHandoffDoesNotPackageASyncedProjectMatchedByDirectoryName(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-20 * time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"mine","timestamp":%q,"cwd":"/work/api",`+
		`"message":{"role":"user","content":"my own retry budget question"}}`, at)
	if err := os.WriteFile(filepath.Join(store, "mine.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	rec := index.SyncRecord{
		Harness: "claude", SessionID: "peer0", Project: "clients/acme/api",
		Role: "user", Text: "the acme ledger cutover and the client contact list",
		Time: time.Now().Add(-time.Hour).UTC(),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}

	work := filepath.Join(tmp, "work", "api")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	// t.Chdir, so the restore happens before the temp directory is removed:
	// on Windows a directory a process is standing in cannot be deleted.
	t.Chdir(work)

	out, err := captureRun(t, "handoff")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "acme") {
		t.Errorf("handoff packaged a synced project matched only by directory name:\n%s", out)
	}
	if !strings.Contains(out, "retry budget") {
		t.Errorf("handoff did not pick this project's own session:\n%s", out)
	}
}
