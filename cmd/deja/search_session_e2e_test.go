package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The two-step loop #1321 asks for: find the session by what was said, then
// search inside it for what was done. Without --session the second step meant
// turning the id back into a file path and grepping it by hand.
func TestSearchInsideOneSession(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, id, user, tool string) {
		lines := `{"type":"user","message":{"role":"user","content":"` + user + `"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n" +
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` + tool + `"}]},"timestamp":"2026-08-02T10:01:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, name), []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jsonl", "aaa11111", "why does the build fail", "go build ./... undefined parseThing")
	write("b.jsonl", "bbb22222", "why does the build fail here too", "go build ./... undefined otherThing")
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	all, err := captureRun(t, "search", "--role", "tool", "--all", "go build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "parseThing") || !strings.Contains(all, "otherThing") {
		t.Fatalf("the fixture does not put work in both sessions:\n%s", all)
	}

	one, err := captureRun(t, "search", "--session", "aaa1", "--role", "tool", "--all", "go build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one, "parseThing") {
		t.Errorf("searching inside the session lost its own work:\n%s", one)
	}
	if strings.Contains(one, "otherThing") {
		t.Errorf("searching inside one session answered from another:\n%s", one)
	}
}

// A search that comes back empty because of a filter has to name the filter:
// "no matches" alone reads as "you have no such memory" (#686, #715, #727).
func TestEmptySearchNamesTheSessionFilter(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the build fails on staging"},"timestamp":"2026-08-02T10:00:00Z","sessionId":"aaa11111","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The advice goes to stderr, where every other "why is this empty" line goes.
	out, err := captureRunStderr(t, "search", "--session", "zzz99999", "build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `session "zzz99999"`) {
		t.Errorf("an empty result does not say a session filter was applied:\n%s", out)
	}
}
