package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An imported session lives only in the index — forget rewrote the log and the
// rebuild has nothing to re-read, so the undo lifted the tombstone and claimed
// a restore that did not happen (#967).
func TestUnforgetSaysWhenAnImportedSessionCannotComeBack(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "peer/api", Role: "user", Text: "peer text"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	var imported string
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "imported-") {
			imported = m.ID
		}
	}
	if imported == "" {
		t.Fatal("no imported session in the index")
	}

	if _, err := captureRunStderr(t, "forget", "--session", imported); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "forget", "--unforget", "claude:"+imported)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "restored 1 session") {
		t.Errorf("the undo claimed a restore that did not happen: %q", out)
	}
	if !strings.Contains(out, "sync import") {
		t.Errorf("the undo does not name the way back: %q", out)
	}

	// A local session still round-trips, and still says so.
	if _, err := captureRunStderr(t, "forget", "--session", "loc"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "forget", "--unforget", "claude:loc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "restored 1 session") {
		t.Errorf("a local session no longer reports its restore: %q", out)
	}
}
