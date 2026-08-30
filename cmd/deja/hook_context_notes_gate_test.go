package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// sessionStartBlock runs the session-start hook the way a harness does and
// returns what it injected.
func sessionStartBlock(t *testing.T, cwd string) string {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = runHookContext(index.DefaultDir(), true)
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, cerr := io.Copy(&out, r); cerr != nil {
		t.Fatal(cerr)
	}
	return out.String()
}

// The standing-decisions block is folded into the cached session-start digest,
// whose gate is the recall mode and the trust policy. A decision the reader has
// just removed with `deja forget` was therefore handed to every session start
// for the rest of the TTL — the one thing the privacy command must not do
// (#2537).
func TestForgettingANoteRetiresTheCachedBlock(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	rec := `{"type":"user","sessionId":"dec","timestamp":"` + at + `","cwd":"/work/app","message":{"role":"user","content":"should the retry budget go up to 10?"}}` + "\n" +
		`{"type":"assistant","sessionId":"dec","timestamp":"` + at + `","cwd":"/work/app","message":{"role":"assistant","content":"no: the retry budget stays at 5"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "dec.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := sources.AppendPromoted("app", "t", "the retry budget stays at 5",
		"claude:dec", "accepted", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Served once, which is what puts it in the cache.
	if got := sessionStartBlock(t, "/work/app"); !strings.Contains(got, "standing decisions in this project") {
		t.Fatalf("the fixture never served the decision:\n%s", got)
	}

	// Removed the way deja's own message tells the reader to remove it.
	if err := os.WriteFile(sources.NotesFile(), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := sessionStartBlock(t, "/work/app"); strings.Contains(got, "standing decisions in this project") {
		t.Errorf("a decision the reader removed is still being served as standing:\n%s", got)
	}
}

// A promotion made a moment ago is the same question the other way round: the
// block has to notice the file growing, not only shrinking.
func TestANewNoteReachesTheNextSessionStart(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	rec := `{"type":"user","sessionId":"dec","timestamp":"` + at + `","cwd":"/work/app","message":{"role":"user","content":"the retry budget question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "dec.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	_ = sessionStartBlock(t, "/work/app")

	if err := sources.AppendPromoted("app", "t", "the retry budget stays at 5",
		"claude:dec", "accepted", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := sessionStartBlock(t, "/work/app"); !strings.Contains(got, "standing decisions in this project") {
		t.Errorf("a decision promoted a moment ago did not reach the next session start:\n%s", got)
	}
}
