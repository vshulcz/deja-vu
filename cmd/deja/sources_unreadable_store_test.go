package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An opencode store that would not open read as a store nobody had used —
// `sessions=0 messages=0` with no note — while every file-store row says
// "cannot be read" for the same thing (#1000, #3190).
func TestSourcesSaysAnOpencodeStoreCouldNotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file the owner cannot read")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"why does the pager stall"}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OPENCODE_DB", db)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	if err := os.Chmod(db, 0); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { printSources(filepath.Join(tmp, "index.db")) })
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "opencode\t") {
			continue
		}
		if !strings.Contains(l, "cannot be read") {
			t.Fatalf("the unreadable store reads as unused: %s", l)
		}
		return
	}
	t.Fatalf("no opencode row in:\n%s", out)
}
