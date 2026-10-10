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

	// An ordinary ambiguous prefix keeps the advice that works there.
	prefix, err := captureRunStderr(t, "show", "ab")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prefix, "longer prefix") {
		t.Errorf("prefix case: %q", prefix)
	}
	if strings.Contains(prefix, "share the id") {
		t.Errorf("a unique id was reported as shared: %q", prefix)
	}

	// A complete id that also starts other ids is that session: no longer
	// prefix reaches it, so it is read, and without the ambiguity note.
	whole, err := captureRunStderr(t, "show", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(whole, "sessions match") {
		t.Errorf("a complete id was reported as ambiguous: %q", whole)
	}
	if out, err := captureRun(t, "show", "abc"); err != nil || !strings.Contains(out, "the short one") {
		t.Errorf("show abc read another session: %v\n%s", err, out)
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

// --harness narrows a prefix to that harness's sessions. The newest match
// across every harness was picked first and then refused, so `show c --harness
// codex` said no session matches while a codex session started with "c", and
// --json answered a prefix that was still ambiguous within the harness.
func TestShowHarnessNarrowsAPrefix(t *testing.T) {
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
	write(filepath.Join(qwen, "abq1.jsonl"),
		`{"type":"user","sessionId":"abq1","timestamp":"2026-07-18T10:00:00Z","message":{"role":"user","parts":[{"text":"older qwen"}]}}`)
	write(filepath.Join(qwen, "abq2.jsonl"),
		`{"type":"user","sessionId":"abq2","timestamp":"2026-07-19T10:00:00Z","message":{"role":"user","parts":[{"text":"newer qwen"}]}}`)
	write(filepath.Join(claude, "abc9.jsonl"),
		`{"type":"user","sessionId":"abc9","cwd":"/w/p","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","content":"newest overall"}}`)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "show", "ab", "--harness", "qwen")
	if err != nil {
		t.Fatalf("a qwen session starts with the prefix: %v", err)
	}
	if !strings.Contains(out, "abq2") {
		t.Fatalf("want the newest qwen match:\n%s", out)
	}
	note, _ := captureRunStderr(t, "show", "ab", "--harness", "qwen")
	if !strings.Contains(note, "2 qwen sessions match") {
		t.Errorf("no ambiguity note within the harness: %q", note)
	}
	if _, err := captureRun(t, "show", "ab", "--harness", "qwen", "--json"); err == nil || !strings.Contains(err.Error(), "--json reads one") {
		t.Errorf("--json answered an ambiguous prefix: %v", err)
	}
	if _, err := captureRun(t, "show", "abq1", "--harness", "qwen", "--json"); err != nil {
		t.Errorf("an exact id with --harness: %v", err)
	}
}
