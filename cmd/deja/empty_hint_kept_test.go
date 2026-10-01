package main

import (
	"os"
	"path/filepath"
	"runtime"
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

// The empty-index gate must not take the permission-denied line with it: with
// one Gemini project indexed and another behind chmod 000, that line is the
// only pointer to the store the pass could not read. It drops the "nothing to
// index" half, since the index still holds sessions (#4221).
func TestDeniedStorePlusNonEmptyIndexStillNamesTheDeniedStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 does not deny a read on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, ".gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	write := func(project, id, word string) string {
		chats := filepath.Join(root, "tmp", project, "chats")
		if err := os.MkdirAll(chats, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"sessionId":"` + id + `","projectHash":"` + project + `","startTime":"2026-10-01T13:56:00.000Z","lastUpdated":"2026-10-01T13:56:10.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-10-01T13:56:01.000Z","type":"user","content":[{"text":"why does the ` + word + ` cache miss"}]}
{"id":"g1","timestamp":"2026-10-01T13:56:02.000Z","type":"gemini","content":"it is cold after every deploy"}
`
		if err := os.WriteFile(filepath.Join(chats, "session-2026-10-01T13-56-"+id[:8]+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(root, "tmp", project)
	}
	write("alpha", "11111111-2b2c-4b24-b439-2a495200e262", "quillfern")
	locked := write("beta", "22222222-2b2c-4b24-b439-2a495200e262", "marrowlace")
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if deniedStoreCount() == 0 {
		t.Skip("chmod 000 left the project readable here")
	}
	index.LastBuild = index.BuildSummary{}
	stderr, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "could not be read (permission denied)") {
		t.Fatalf("the denied store went unnamed:\n%s", stderr)
	}
	for _, wrong := range []string{"nothing to index", "nothing is left"} {
		if strings.Contains(stderr, wrong) {
			t.Fatalf("the index holds sessions and stderr says %q:\n%s", wrong, stderr)
		}
	}
}
