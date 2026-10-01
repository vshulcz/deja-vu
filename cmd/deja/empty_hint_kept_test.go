package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The only transcript on disk was deleted: the index keeps the session
// searchable and says so, and must not add that no history was found (#4221).
// The shape the bug was seen in: a Gemini CLI store after --delete-session.
func TestIndexDoesNotCallKeptHistoryMissing(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, ".gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	chats := filepath.Join(root, "tmp", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chats, "session-2026-10-01T13-56-22931634.jsonl")
	body := `{"sessionId":"22931634-2b2c-4b24-b439-2a495200e262","projectHash":"abc","startTime":"2026-10-01T13:56:00.000Z","lastUpdated":"2026-10-01T13:56:10.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-10-01T13:56:01.000Z","type":"user","content":[{"text":"why does the quillfern cache miss"}]}
{"id":"g1","timestamp":"2026-10-01T13:56:02.000Z","type":"gemini","content":"it is cold after every deploy"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The next `deja index` is a new process: nothing carried from this one.
	index.LastBuild = index.BuildSummary{}
	stderr, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "no longer on disk") {
		t.Fatalf("control: the pass did not report the deleted transcript:\n%s", stderr)
	}
	if strings.Contains(stderr, "nothing to index yet") {
		t.Fatalf("the index holds the session and still says nothing was found:\n%s", stderr)
	}
}
