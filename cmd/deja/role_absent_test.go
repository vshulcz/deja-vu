package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// storeWith writes one session and indexes it. withWork adds a tool result, so
// the same fixture can be a store that records the work and one that does not.
func storeWith(t *testing.T, withWork bool) {
	t.Helper()
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","message":{"role":"user","content":"why does the build fail on staging"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	if withWork {
		lines += `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"go build ./... : undefined parseThing"}]},"timestamp":"2026-08-02T10:01:00Z","sessionId":"aaa","cwd":"/proj"}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}
}

// deja says it indexes the work and not only the talk. On a store whose harness
// records no tool calls, `--role tool` answered exactly the way a bad query
// does, so a transcript that was missing half of what happened looked complete
// (#1321).
func TestRoleSearchSaysWhenTheStoreHoldsNoneOfThatKind(t *testing.T) {
	storeWith(t, false)
	out, err := captureRunStderr(t, "search", "--role", "tool", "build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no tool records") {
		t.Errorf("an empty role search does not say the kind is absent:\n%s", out)
	}
}

// When the store does hold them, the line must not appear: a query that simply
// missed is a different answer, and saying both would teach the reader to
// ignore it.
func TestRoleSearchStaysQuietWhenTheKindExists(t *testing.T) {
	storeWith(t, true)
	out, err := captureRunStderr(t, "search", "--role", "tool", "nosuchtermanywhere")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no tool records") {
		t.Errorf("the note fired on a store that has tool records:\n%s", out)
	}
}

// And it stays out of a search that found something.
func TestRoleSearchWithHitsSaysNothingExtra(t *testing.T) {
	storeWith(t, true)
	out, err := captureRunStderr(t, "search", "--role", "tool", "parseThing")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no tool records") {
		t.Errorf("the note fired on a search that matched:\n%s", out)
	}
}
