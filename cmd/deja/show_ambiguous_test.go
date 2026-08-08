package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "Use a longer prefix" cannot be followed when the ids are the same string;
// naming the harness (harness:id) is the only thing that separates them (#719).
func TestShowNamesHarnessWhenIDsAreShared(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude", "proj-p")
	qwen := filepath.Join(tmp, "qwen", "projects", "proj-z", "chats")
	for _, d := range []string{claude, qwen} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claude, "abc12345.jsonl"),
		`{"type":"user","sessionId":"abc12345","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"claude side"}}`)
	write(filepath.Join(claude, "abc99999.jsonl"),
		`{"type":"user","sessionId":"abc99999","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"other claude session"}}`)
	write(filepath.Join(qwen, "abc12345.jsonl"),
		`{"type":"user","sessionId":"abc12345","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","parts":[{"text":"qwen side"}]}}`)
	// A session whose whole id is also a prefix of the others: the prefix is
	// ambiguous, the id is not, and only the longer-prefix advice fits.
	write(filepath.Join(claude, "abc.jsonl"),
		`{"type":"user","sessionId":"abc","cwd":"/w/p","timestamp":"2026-07-19T10:00:00Z","message":{"role":"user","content":"the short one"}}`)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	shared, err := captureRunStderr(t, "show", "abc12345")
	if err != nil {
		t.Fatal(err)
	}
	// The advice names the harness:id form, which every selector-resolving
	// command accepts — promote/handoff/resume/share reject --harness (#872).
	if !strings.Contains(shared, "share the id") || !strings.Contains(shared, "claude:abc12345 or qwen:abc12345") {
		t.Errorf("shared id: %q", shared)
	}
	if strings.Contains(shared, "--harness") {
		t.Errorf("still advised a flag some commands reject: %q", shared)
	}
	if strings.Contains(shared, "longer prefix") {
		t.Errorf("still advised a longer prefix: %q", shared)
	}

	// An ordinary ambiguous prefix keeps the advice that works there — even
	// when that prefix is itself one session's whole id.
	prefix, err := captureRunStderr(t, "show", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prefix, "longer prefix") {
		t.Errorf("prefix case: %q", prefix)
	}
	if strings.Contains(prefix, "share the id") {
		t.Errorf("a unique id was reported as shared: %q", prefix)
	}

	// One match, no chatter.
	quiet, err := captureRunStderr(t, "show", "abc99999")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "sessions") {
		t.Errorf("unambiguous id said %q", quiet)
	}
}
