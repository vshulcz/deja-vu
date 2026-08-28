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

// An unset shell variable arrives as an empty argument. Every other selector
// command refuses one; share printed a shareable digest of whichever session
// deja reached first, and ctx printed its transcript (#2259).
func TestAnEmptySelectorIsNotASession(t *testing.T) {
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
	for i := 1; i <= 2; i++ {
		line := fmt.Sprintf(`{"type":"user","sessionId":"sess%d","timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"gateway_timeout on the reconnect_loop number %d"}}`, i, at, i)
		name := fmt.Sprintf("s%d.jsonl", i)
		if err := os.WriteFile(filepath.Join(claude, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	// The premise: both commands work when they are given a real selector.
	if err := runShare(dir, []string{"sess1"}, &out); err != nil || !strings.Contains(out.String(), "sess1") {
		t.Fatalf("share of a real id: %v %q", err, out.String())
	}
	out.Reset()
	if err := cmdCtx(dir, []string{"sess1"}); err != nil {
		t.Fatalf("ctx of a real id: %v", err)
	}

	out.Reset()
	err := runShare(dir, []string{""}, &out)
	if err == nil {
		t.Errorf("share of an empty selector printed: %q", out.String())
	} else if !strings.Contains(err.Error(), "id-prefix") {
		t.Errorf("share of an empty selector said %v", err)
	}

	if err := cmdCtx(dir, []string{""}); err == nil {
		t.Error("ctx of an empty selector was answered")
	} else if !strings.Contains(err.Error(), "id-prefix") {
		t.Errorf("ctx of an empty selector said %v", err)
	}
	// Space is the same nothing.
	out.Reset()
	if err := runShare(dir, []string{"  "}, &out); err == nil {
		t.Errorf("share of a blank selector printed: %q", out.String())
	}
}
