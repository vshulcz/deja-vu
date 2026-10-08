package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Cursor keeps each CLI chat in a directory of its own, so deleting a chat
// takes the directory with the transcript, and the pass read that as a store
// that went away and dropped the session — while the same deletion of a
// Claude transcript keeps it (#2970, #4195).
func TestADeletedSessionDirectoryStaysInTheIndex(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor-cli")
	chats := filepath.Join(cli, "projects", "Users-me-app", "agent-transcripts")
	write := func(id, text string) string {
		p := filepath.Join(chats, id, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"`+text+`"}]}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gone := "bbbbbbbb-1111-4222-8333-444444444444"
	write(gone, "the quarnex cache went stale")
	write("cccccccc-1111-4222-8333-444444444444", "beta stable")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(filepath.Join(chats, gone)); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "cursor", false, &log); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "quarnex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != gone {
		t.Fatalf("a chat deleted with its directory left the index: %#v\nlog: %s", ss, log.String())
	}
	if !strings.Contains(log.String(), "still searchable") || strings.Contains(log.String(), "not mounted") {
		t.Errorf("the pass did not say the chat was kept:\n%s", log.String())
	}

	// A rebuild keeps it too: it reads the kept records back from the old
	// store rather than from a transcript that is no longer there.
	if err := Ensure(dir, "cursor", true, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "quarnex"}); len(ss) != 1 {
		t.Fatalf("a rebuild dropped the deleted chat: %#v", ss)
	}

	// The store itself going away is still an uninstall or a move.
	if err := os.RemoveAll(filepath.Join(cli, "projects")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "stable"}); len(ss) != 0 {
		t.Fatalf("a store that went away whole stayed in the index: %#v", ss)
	}
}

// Only a directory named for one session counts. An encoded working directory
// under a store root carries a UUID whenever the directory did, and deleting
// that folder is a project going away, as before.
func TestSessionDirNames(t *testing.T) {
	const id = "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e8a"
	for name, want := range map[string]bool{
		id:                        true, // Cursor, Copilot CLI, Antigravity, a Claude sidecar
		"session_" + id:           true, // Kimi
		"session-" + id:           true, // DeepSeek
		"sess_" + id:              true, // Kiro
		"1791454692202_zb19s":     true, // Cline CLI
		"1791454692202":           false,
		"-private-var-tmp-" + id:  false,
		"Users-me-" + id + "-app": false,
		"projects":                false,
	} {
		if got := sessionDirName.MatchString(name); got != want {
			t.Errorf("%q: %v, want %v", name, got, want)
		}
	}
}

// A chat moved with its directory and edited on the way is not caught by the
// rename pairing, and keeping the old copy as a deletion made it its own
// second copy.
func TestAMovedSessionDirectoryIsNotKeptTwice(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor-cli")
	id := "bbbbbbbb-1111-4222-8333-444444444444"
	at := func(project, text string) string {
		p := filepath.Join(cli, "projects", project, "agent-transcripts", id, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"`+text+`"}]}}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	at("Users-me-app", "the vorplex migration stalled")
	at("Users-me-other", "beta stable")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "no-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cli, "projects", "Users-me-app", "agent-transcripts", id)); err != nil {
		t.Fatal(err)
	}
	at("Users-me-app2", "the vorplex migration resumed")
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "stalled"}); len(ss) != 0 {
		t.Fatalf("the moved chat's old copy stayed beside the new one: %#v", ss)
	}
	if ss, _ := Search(dir, search.Options{Query: "resumed"}); len(ss) != 1 {
		t.Fatalf("the moved chat is not there once: %#v", ss)
	}
	// Nor does a rebuild bring the old copy back.
	if err := Ensure(dir, "cursor", true, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "stalled"}); len(ss) != 0 {
		t.Fatalf("a rebuild kept the moved chat's old copy: %#v", ss)
	}
}
