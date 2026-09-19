package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The line the replacement-grade incremental pass prints is on the path every
// user hits: the first search or blame after a day's work prints it above the
// answer. It read "incremental index changed_files=306 removed_files=0
// sessions=1789" — a log line, in a stream where every other line is a
// sentence. This pins the register, not the exact wording.
func TestTheIncrementalPassSpeaksInASentence(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "claude", "projects", "-work-pool")
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude", "projects"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	session := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"` + text + `"}}
{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"the pool retires connections before the proxy does"}}
`
	}

	write("aaa1.jsonl", session("aaa1", "connections die five minutes after a deploy"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Rewritten rather than appended to: the tail cannot be resumed, so the
	// next pass is replacement-grade, which is the one that prints this line.
	write("aaa1.jsonl", session("aaa1", "connections die four minutes after a deploy"))
	write("bbb2.jsonl", session("bbb2", "the reindex job runs out of memory at eighty percent"))
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	out := progress.String()

	for _, leak := range []string{"changed_files=", "removed_files=", "sessions="} {
		if strings.Contains(out, leak) {
			t.Errorf("the pass printed %q, which is a log line rather than something a reader was meant to see:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "transcript") {
		t.Errorf("the pass said nothing about the transcripts it re-read:\n%s", out)
	}
	// Whatever it says, it says it once and about a real count: zero is left
	// out rather than printed as "0 gone".
	if strings.Contains(out, " 0 ") {
		t.Errorf("the pass printed a zero count:\n%s", out)
	}
}
