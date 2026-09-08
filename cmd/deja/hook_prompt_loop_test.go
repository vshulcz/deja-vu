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

// The prompt that arrives every minute. Each tick is a new call with the same
// text, and the cooldowns are counted per session shown — so the hook worked
// down the ranking, one session per tick, and answered a loop with sessions
// that had nothing to do with it (#3189).
func TestALoopedPromptIsAnsweredOnce(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	// Several sessions that all carry the subject, which is what gives the
	// ranking somewhere to walk to on the second tick.
	answers := []struct{ subject, settled string }{
		{"dial deadline", "the zebraquux fetcher timed out on a 200ms dial deadline; we raised it to 5s"},
		{"retry budget", "the zebraquux retry budget was two, so the fetcher gave up early"},
		{"dns", "zebraquux resolved slowly and the fetcher timed out waiting on DNS"},
		{"connection pool", "the fetcher shares a pool with zebraquux, so one slow call times out the rest"},
	}
	for i, a := range answers {
		line := fmt.Sprintf(`{"type":"user","sessionId":"zeb%d","timestamp":%q,`+
			`"message":{"role":"user","content":"looking at the zebraquux %s again"}}`+"\n"+
			`{"type":"assistant","sessionId":"zeb%d","timestamp":%q,`+
			`"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, i, at, a.subject, i, at, a.settled)
		if err := os.WriteFile(filepath.Join(claude, fmt.Sprintf("zeb%d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// And the session the question was actually asked in, which is what the
	// first tick is answered with.
	asked := fmt.Sprintf(`{"type":"user","sessionId":"zebmain","timestamp":%q,`+
		`"message":{"role":"user","content":"why does the zebraquux fetcher time out?"}}`+"\n"+
		`{"type":"assistant","sessionId":"zebmain","timestamp":%q,`+
		`"message":{"role":"assistant","content":[{"type":"text","text":`+
		`"we settled it: the zebraquux dial deadline goes to 5s and the retry budget to three"}]}}`, at, at)
	if err := os.WriteFile(filepath.Join(claude, "zebmain.jsonl"), []byte(asked+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(tmp, "work", "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	tick := func(prompt string) string {
		t.Helper()
		var out bytes.Buffer
		payload := fmt.Sprintf(`{"session_id":"loop-1","cwd":%q,"prompt":%q}`, cwd, prompt)
		if err := runHookPrompt(dir, strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	const q = "why does the zebraquux fetcher time out?"
	first := tick(q)
	if !strings.Contains(first, "zebraquux") {
		t.Fatalf("the first tick recalled nothing, so this measures nothing: %q", first)
	}
	for i := 2; i <= 4; i++ {
		if got := tick(q); strings.Contains(got, "deja-recall") {
			t.Errorf("tick %d answered the same question again: %q", i, got)
		}
	}
	// A different question in the same reader is still a question.
	if got := tick("what did we settle on for the zebraquux retry budget?"); !strings.Contains(got, "deja-recall") {
		t.Errorf("the next question went unanswered: %q", got)
	}
}
