package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeContinueStore(t *testing.T, root string, session any, list any) string {
	t.Helper()
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "8f1c2a3e-0000-4000-8000-000000000000.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if list != nil {
		lb, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sessions.json"), lb, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// Continue runs in VS Code and JetBrains and writes one document per session,
// with the date and the workspace in a list beside it — the document itself
// carries neither (#3062).
func TestContinueReadsASessionAndItsListEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	sid := "8f1c2a3e-0000-4000-8000-000000000000"
	path := writeContinueStore(t, root,
		map[string]any{
			"sessionId": sid,
			"history": []any{
				map[string]any{"message": map[string]any{"role": "user", "content": "the retry loop drops the last attempt"}},
				map[string]any{"message": map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "text", "text": "the bound is < where it needs <="},
					map[string]any{"type": "text", "text": "changed it and added a test"},
				}}},
				// System prompts are configuration, and a tool result arrives
				// under the assistant item that asked for it.
				map[string]any{"message": map[string]any{"role": "system", "content": "you are Continue"}},
			},
		},
		[]any{map[string]any{
			"sessionId": sid, "title": "retry loop drops the last attempt",
			"dateCreated": "2026-08-02T10:00:00.000Z", "workspaceDirectory": "/w/api",
		}})

	if files := ContinueSessionFiles(); len(files) != 1 || files[0] != path {
		t.Fatalf("session files = %#v, want just the session document", files)
	}
	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	s := ss[0]
	if s.Harness != "continue" || s.ID != sid {
		t.Fatalf("session = %s/%s, want continue/%s", s.Harness, s.ID, sid)
	}
	// The same project name every path-encoded harness gets: the last two
	// segments, which is what tells /w/api from /other/api.
	if s.Project != "w/api" {
		t.Fatalf("project = %q, want the workspace's own name", s.Project)
	}
	if s.Title != "retry loop drops the last attempt" {
		t.Fatalf("title = %q, want the list entry's", s.Title)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the user turn and the assistant turn: %#v", len(s.Messages), s.Messages)
	}
	if !strings.Contains(s.Messages[1].Text, "needs <=") || !strings.Contains(s.Messages[1].Text, "added a test") {
		t.Fatalf("the array-of-parts content did not come through: %q", s.Messages[1].Text)
	}
	// The list is the only date there is.
	want := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	if !s.Messages[0].Time.Equal(want) {
		t.Fatalf("first turn at %s, want the session's dateCreated %s", s.Messages[0].Time, want)
	}
	if !s.Messages[1].Time.After(s.Messages[0].Time) {
		t.Fatalf("the turns are not in order: %s then %s", s.Messages[0].Time, s.Messages[1].Time)
	}
}

// sessions.json sits in the same directory and is the index of the others, not
// a conversation; reading it as one would file every session's title as a
// session of its own.
func TestContinueSkipsTheListFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	writeContinueStore(t, root,
		map[string]any{"sessionId": "s1", "history": []any{
			map[string]any{"message": map[string]any{"role": "user", "content": "hello"}},
		}},
		[]any{map[string]any{"sessionId": "s1", "dateCreated": "2026-08-02T10:00:00.000Z"}})
	for _, f := range ContinueSessionFiles() {
		if filepath.Base(f) == "sessions.json" {
			t.Fatalf("the list file was offered as a session: %s", f)
		}
	}
}

// Without a list entry there is still a session: the file's mtime is its date,
// and the project falls back to the harness name rather than to the empty
// string, which reads as "no project" everywhere downstream.
func TestContinueWithoutAListEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CONTINUE_ROOT", root)
	path := writeContinueStore(t, root,
		map[string]any{"sessionId": "8f1c2a3e-0000-4000-8000-000000000000", "history": []any{
			map[string]any{"message": map[string]any{"role": "user", "content": "no list beside me"}},
		}}, nil)
	ss, err := ParseContinueFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	if ss[0].Project != "continue" {
		t.Fatalf("project = %q, want the fallback", ss[0].Project)
	}
	if ss[0].Messages[0].Time.IsZero() {
		t.Fatal("the turn has no time, so it sorts as the beginning of the epoch")
	}
}

// CONTINUE_GLOBAL_DIR is Continue's own override, and a machine that sets it
// keeps its sessions nowhere else.
func TestContinueHonoursItsOwnGlobalDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CONTINUE_ROOT", "")
	elsewhere := filepath.Join(home, "elsewhere")
	t.Setenv("CONTINUE_GLOBAL_DIR", elsewhere)
	if got := ContinueRoot(); got != elsewhere {
		t.Fatalf("ContinueRoot = %q, want %q", got, elsewhere)
	}
}
