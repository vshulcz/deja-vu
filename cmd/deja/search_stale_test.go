package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A search must not pay for a rewrite of the whole index. When a store deja
// cannot append to has changed, the answer comes from the index as it stands
// and the refresh is detached — on a 1.7 GB store the old behaviour turned a
// query that matched nothing into a 194 s wait (#1521). A store deja *can*
// append to stays inline, because reading the new bytes is cheap.
func TestSearchDoesNotWaitForARewrite(t *testing.T) {
	tmp := hermeticEnv(t)

	claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(claude, "s1.jsonl")
	line := func(text, stamp string) string {
		return `{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + stamp + `","sessionId":"s1","cwd":"/proj"}` + "\n"
	}
	first := line("staleneedle the first turn", "2026-07-02T10:00:00Z")
	if err := os.WriteFile(transcript, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}

	aiderDir := filepath.Join(tmp, "aider")
	if err := os.MkdirAll(aiderDir, 0o755); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(aiderDir, ".aider.chat.history.md")
	if err := os.WriteFile(history, []byte("# aider chat started at 2026-07-02 10:00:00\n\n#### rewriteneedle first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Claude appends: cheap, so it stays on the search thread and the new turn
	// is in this very answer.
	if err := os.WriteFile(transcript, []byte(first+line("appendedneedle the second turn", "2026-07-02T10:05:00Z")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRunStderr(t, "search", "appendedneedle")
	if err != nil {
		t.Fatalf("search after append: %v\n%s", err, out)
	}
	if strings.Contains(out, "refreshing in the background") {
		t.Errorf("an append was deferred to the warmup instead of run inline:\n%s", out)
	}

	// aider has no append path, so its growth is rewrite-grade work.
	body := "# aider chat started at 2026-07-02 10:00:00\n\n#### rewriteneedle first\n\n#### deferredneedle second\n"
	if err := os.WriteFile(history, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = captureRunStderr(t, "search", "deferredneedle")
	if err != nil && !strings.Contains(err.Error(), "no session matches") {
		t.Fatalf("search during a rewrite: %v\n%s", err, out)
	}
	if !strings.Contains(out, "refreshing in the background") {
		t.Errorf("the search waited for the rewrite:\n%s", out)
	}

	// --rebuild is the escape hatch: someone who asked for the rebuild waits
	// for it and sees the result.
	out, err = captureRunStderr(t, "search", "--rebuild", "deferredneedle")
	if err != nil {
		t.Fatalf("rebuild search: %v\n%s", err, out)
	}
	if strings.Contains(out, "refreshing in the background") {
		t.Errorf("--rebuild deferred the work it was asked to do:\n%s", out)
	}
}
