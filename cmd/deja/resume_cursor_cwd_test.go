package main

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// seedCursorChat lays out a CLI chat the way cursor-agent does: the transcript
// under projects/<folder with every non-alphanumeric blanked>, and meta.json
// under chats/<md5 of the cwd>/<id>.
func seedCursorChat(t *testing.T, cli, cwd, id string) string {
	t.Helper()
	folder := strings.TrimLeft(regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-"), "-")
	tp := filepath.Join(cli, "projects", folder, "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(tp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tp, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"hello there"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte(cwd))
	chat := filepath.Join(cli, "chats", hex.EncodeToString(sum[:]), id)
	if err := os.MkdirAll(chat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chat, "meta.json"), []byte(`{"schemaVersion":1,"cwd":`+jsonString(cwd)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chat, "store.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return tp
}

// The folder name cannot be read back for a directory with a dot, a space or
// a non-ASCII name, and cursor-agent finds a chat only from the directory it
// ran in, so resume has to take that directory from the chat's meta.json
// (#4193).
func TestResumeCursorTakesTheDirectoryFromTheChat(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("CURSOR_CONFIG_DIR", cli)
	for i, name := range []string{"my.app", "проект", "two words"} {
		cwd := filepath.Join(tmp, "work", name)
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		id := "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e8" + string(rune('0'+i))
		tp := seedCursorChat(t, cli, cwd, id)

		dir, cmd, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Path: tp})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dir != cwd || cmd != "cursor-agent --resume "+id {
			t.Errorf("%s: dir=%q cmd=%q, want %q", name, dir, cmd, cwd)
		}
		ss, err := sources.ParseCursorTranscript(tp)
		if err != nil || len(ss) != 1 || ss[0].Project != "work/"+name {
			t.Errorf("%s: project = %v %v", name, ss, err)
		}
	}

	// The directory is gone: there is no command that reopens the chat, and
	// printing one sends someone into an empty session.
	cwd := filepath.Join(tmp, "work", "gone.app")
	id := "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e99"
	tp := seedCursorChat(t, cli, cwd, id)
	if _, _, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Path: tp}); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a chat whose directory is gone: err = %v", err)
	}

	// The chat is no longer in cursor-agent's store although the directory is
	// there: the command would open an empty chat.
	cwd = filepath.Join(tmp, "work", "kept.app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	id = "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e98"
	tp = seedCursorChat(t, cli, cwd, id)
	if err := os.Remove(filepath.Join(cli, "chats", sources.CursorChatBucket(cwd), id, "store.db")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Path: tp}); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a chat gone from the store: err = %v", err)
	}
}

// A meta.json under a folder that is not the md5 of its cwd is a copy: the
// project can come from it, the cd cannot, because cursor-agent would look in
// a different folder.
func TestResumeCursorTrustsOnlyAChatFiledUnderItsDirectory(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("CURSOR_CONFIG_DIR", cli)
	cwd := filepath.Join(tmp, "work", "my.app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e97"
	tp := seedCursorChat(t, cli, cwd, id)
	right := filepath.Join(cli, "chats", sources.CursorChatBucket(cwd))
	if err := os.Rename(right, filepath.Join(cli, "chats", "0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	if got, verified := sources.CursorChatCWD(tp); got != cwd || verified {
		t.Fatalf("CursorChatCWD = %q %v, want %q unverified", got, verified, cwd)
	}
	if _, _, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Path: tp}); err == nil {
		t.Fatal("resume printed a cd into a directory cursor-agent would not find the chat under")
	}
}
