package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Two transcripts can write the same harness:id, and then one manifest row
// holds both conversations. The build says so once (#698); forget said
// "1 session" and took two, from two projects (#970).
func TestForgetSaysWhenARowCoversTwoConversations(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, p := range []struct{ dir, cwd, text string }{
		{"-proj-a", "/proj-a", "FIRST conversation about the ticker"},
		{"-proj-b", "/proj-b", "SECOND conversation about the vault"},
	} {
		store := filepath.Join(root, p.dir)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		rec := `{"type":"user","message":{"role":"user","content":"` + p.text + `"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"dup","cwd":"` + p.cwd + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, "dup.jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "forget", "--session", "dup", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "more than one conversation") {
		t.Errorf("the dry run does not say the row holds two conversations:\n%s", out)
	}
	out, err = captureRun(t, "forget", "--session", "dup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "more than one conversation") {
		t.Errorf("forget does not say what it took:\n%s", out)
	}

	// A row that holds one conversation says nothing extra.
	store := filepath.Join(root, "-solo")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a solo conversation"},"timestamp":"2026-07-12T10:00:00Z","sessionId":"solo","cwd":"/solo"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "solo.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "forget", "--session", "solo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "more than one conversation") {
		t.Errorf("an ordinary session was called a shared row:\n%s", out)
	}
}
