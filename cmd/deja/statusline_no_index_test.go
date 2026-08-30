package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The statusline is the one surface a person watches all day, and it already
// speaks up for a build in flight and for a rule that turns memory off. An
// index that is gone — a cleared cache, a moved directory, an unmounted volume
// — left it reporting an ordinary day while recall could answer nothing (#2419).
func TestTheStatuslineSaysWhenThereIsNoIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-08-28T10:00:00Z","cwd":"/work/app",` +
		`"message":{"role":"user","content":"the retry budget on main"}}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: with an index in place the line is the ordinary one.
	var ok bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(`{"session_id":"s1","cwd":"/work/app"}`), &ok); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ok.String(), "index") {
		t.Fatalf("a healthy machine already talks about the index: %q", ok.String())
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	var gone bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(`{"session_id":"s1","cwd":"/work/app"}`), &gone); err != nil {
		t.Fatal(err)
	}
	got := gone.String()
	if strings.Contains(got, "recalls yet today") || strings.Contains(got, "injected") {
		t.Errorf("the index is gone and the line reads as a quiet day: %q", got)
	}
	if !strings.Contains(got, "index") {
		t.Errorf("the line says nothing about the index: %q", got)
	}
}
