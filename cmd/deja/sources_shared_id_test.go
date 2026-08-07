package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two transcripts can carry one conversation id: a harness mid-migration writes
// the same session to its old and new store, and a resumed run continues an id
// in a second file. The index keys on harness+id and holds one session for the
// pair — `deja index` even says so — while `deja sources`, the row people read
// to size their history, counted transcripts and claimed two.
func TestSourcesCountsOneConversationOnce(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-workspace-demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text, stamp string) {
		line := `{"type":"user","sessionId":"resumed-1","timestamp":"` + stamp +
			`","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("session.jsonl", "start the migration", "2026-07-17T09:00:00Z")
	write("resume.jsonl", "continue the migration", "2026-07-17T10:00:00Z")

	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	line := storeLine(out, "claude")
	if !strings.Contains(line, "sessions=1 ") {
		t.Errorf("one conversation in two transcripts is not counted once: %q", line)
	}
	if !strings.Contains(line, "messages=2 ") {
		t.Errorf("both transcripts should still be read: %q", line)
	}
}
