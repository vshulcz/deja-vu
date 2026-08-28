package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A mistyped harness must not be reported as a missing session: search, last
// and blame all say which value they did not recognise, while show blamed the
// id the reader typed correctly and resume looked the harness up as the id
// (#2251).
func TestAMistypedHarnessIsNamedByTheCommandsThatTakeAnID(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"sess1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"the retry budget for uploads"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: the session is there under its real harness.
	if err := cmdShow(dir, []string{"sess1", "--harness", "claude"}, ""); err != nil {
		t.Fatalf("the session cannot be shown at all: %v", err)
	}

	err := cmdShow(dir, []string{"sess1", "--harness", "cluade"}, "")
	if err == nil {
		t.Fatal("show accepted a harness that does not exist")
	}
	if !strings.Contains(err.Error(), "cluade") || !strings.Contains(err.Error(), "not a harness") {
		t.Errorf("show said %v — it should name the harness it did not recognise", err)
	}

	var out bytes.Buffer
	err = runResume(dir, []string{"sess1", "--harness", "cluade"}, &out)
	if err == nil {
		t.Fatal("resume accepted a flag it does not take")
	}
	if strings.Contains(err.Error(), `matches "cluade"`) {
		t.Errorf("resume looked the flag value up as the id: %v", err)
	}
	if !strings.Contains(err.Error(), "--harness") {
		t.Errorf("resume said %v — it should name the flag it does not take", err)
	}
	// A second bare word is the same mistake without a dash: it used to replace
	// the id silently.
	if err := runResume(dir, []string{"sess1", "extra"}, &out); err == nil ||
		strings.Contains(err.Error(), `matches "extra"`) {
		t.Errorf("resume with a stray word: %v", err)
	}

	// handoff had the same shape, found by running the same input at it.
	if err := runHandoff(dir, []string{"sess1", "extra"}, &out); err == nil ||
		strings.Contains(err.Error(), `matches "extra"`) {
		t.Errorf("handoff with a stray word: %v", err)
	}
	// And it still hands off the session it was given.
	out.Reset()
	if err := runHandoff(dir, []string{"sess1"}, &out); err != nil {
		t.Errorf("handoff of a real id: %v", err)
	}
}
