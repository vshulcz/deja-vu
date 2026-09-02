package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Claude Code deletes a transcript once it is older than cleanupPeriodDays
// (30 by default), and the next incremental pass dropped the session with it:
// the one client-side housekeeping deja exists to outlast took the index down
// with the file. A file that is gone while its store is still there is that
// cleanup, or a deletion by hand — and `deja forget` is the deliberate path,
// so the index keeps what it indexed (#2970).
func TestADeletedTranscriptStaysInTheIndex(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := filepath.Join(proj, "s1.jsonl")
	s2 := filepath.Join(proj, "s2.jsonl")
	if err := os.WriteFile(s1, []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"the zorblax pool deadlocked"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s2, []byte(`{"type":"user","sessionId":"s2","timestamp":"2026-08-02T03:04:05Z","message":{"role":"user","content":"beta stable"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	// The cleanup: one file gone, the store around it still there.
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "zorblax"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("a transcript the client deleted left the index with it: %#v\nlog: %s", ss, log.String())
	}
	if !strings.Contains(log.String(), "still searchable") {
		t.Errorf("the pass kept the session and said nothing about it:\n%s", log.String())
	}
	// And it stays kept on the pass after that, when there is nothing else to do.
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Fatalf("the kept session went away on the following pass: %#v", ss)
	}
	// And on the pass that appends to a neighbour — the cheap path, which
	// parses nothing but the grown file — the kept session is still there
	// and the pass still says so.
	f, err := os.OpenFile(s2, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assistant","sessionId":"s2","timestamp":"2026-08-02T03:05:05Z","message":{"role":"assistant","content":"gamma appended"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	log.Reset()
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Fatalf("the kept session went away on an append pass: %#v", ss)
	}
	if !strings.Contains(log.String(), "still searchable") {
		t.Errorf("the append pass kept the session and said nothing about it:\n%s", log.String())
	}
	// A session with the same id arriving from another project is a collision
	// (#699), not the kept transcript being renamed: both stay.
	other := filepath.Join(claudeRoot, "-Users-shulcz-other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "s1.jsonl"), []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-08-03T03:04:05Z","message":{"role":"user","content":"unrelated quux work"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Fatalf("a same-id session from another project took the kept one with it: %#v", ss)
	}
	if ss, _ := Search(dir, search.Options{Query: "quux"}); len(ss) != 1 {
		t.Fatalf("the colliding session from the other project was not indexed: %#v", ss)
	}

	// The other case is unchanged: the whole store gone is not a cleanup —
	// it is an uninstall, a move, or a disk that is not there — and a tree
	// that is not a mount point is dropped as before.
	if err := os.RemoveAll(claudeRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "stable"}); len(ss) != 0 {
		t.Fatalf("a store that went away whole was kept: %#v", ss)
	}
}
