package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A page that holds both copies of one session — this machine's and the one
// that arrived by sync — has to say they are the same session, or the reader
// is looking at two answers to one question with no way to tell (#1775).
func TestRecallNamesTheOtherMachinesCopy(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(id, ts, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"),
		[]byte(line("dup-2", "2026-08-20T01:00:00Z", "quazzle local copy: the limit is five")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The same session as it arrived from another machine.
	imported := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(imported, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{
		Harness: "claude", SessionID: "dup-2", Project: "proj",
		Role: "user", Text: "quazzle other machine: the limit is nine",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imported, "deja-sync-elsewhere-1.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", imported); err != nil {
		t.Fatal(err)
	}

	text, _, _, _, err := recallTextResult(dir, "quazzle limit", "", 5, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "five") || !strings.Contains(text, "nine") {
		t.Fatalf("the fixture is wrong, both copies should be on the page:\n%s", text)
	}
	if !strings.Contains(text, "the same session") {
		t.Errorf("the page does not say the two are one session:\n%s", text)
	}
	// Each copy is named from its own side rather than with one hedged
	// sentence on both.
	if n := strings.Count(text, "may not say the same thing"); n != 2 {
		t.Errorf("the marker appears %d times, want one per copy:\n%s", n, text)
	}
	if !strings.Contains(text, "another machine's copy") || !strings.Contains(text, "this machine's copy") {
		t.Errorf("the two copies are not told apart:\n%s", text)
	}
}
