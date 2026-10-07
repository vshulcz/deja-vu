package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func aiderReadHome(t *testing.T) (home, proj string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AIDER_READ", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("AIDER_GIT", "")
	proj = filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "read:\n  - " + aiderContextPath() + "\n"
	if err := os.WriteFile(aiderConfPath(), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, proj
}

// A project .aider.conf.yml with its own read: list hid the home one, and with
// it deja's context file: aider takes one list, the first it finds (#4801).
func TestAiderReadArgsKeepsTheProjectList(t *testing.T) {
	_, proj := aiderReadHome(t)
	ctx := aiderContextPath()
	if got := aiderReadArgs(nil, proj, ctx); got != nil {
		t.Fatalf("the home list already reads the file, want no args, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(proj, ".aider.conf.yml"), []byte("model: x\nread: [CONVENTIONS.md, 'docs/a b.md']  # mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"--read", "CONVENTIONS.md", "--read", "docs/a b.md", "--read", ctx}
	if got := aiderReadArgs(nil, proj, ctx); !slices.Equal(got, want) {
		t.Errorf("project list: got %q, want %q", got, want)
	}
	// Block form, and a second key: PyYAML keeps the last.
	if err := os.WriteFile(filepath.Join(proj, ".aider.conf.yml"), []byte("read: old.md\nread:\n  # comment\n  - CONVENTIONS.md\n  - \"x.md\"\nmodel: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want = []string{"--read", "CONVENTIONS.md", "--read", "x.md", "--read", ctx}
	if got := aiderReadArgs(nil, proj, ctx); !slices.Equal(got, want) {
		t.Errorf("block list: got %q, want %q", got, want)
	}
	// A list given on the command line is added to, not repeated.
	if got := aiderReadArgs([]string{"--read=n.md"}, proj, ctx); !slices.Equal(got, []string{"--read", ctx}) {
		t.Errorf("command line: got %q", got)
	}
	// AIDER_READ outranks every config file.
	t.Setenv("AIDER_READ", `["e.md"]`)
	if got := aiderReadArgs(nil, proj, ctx); !slices.Equal(got, []string{"--read", "e.md", "--read", ctx}) {
		t.Errorf("AIDER_READ: got %q", got)
	}
}

// The live file stands in for the shared one: the home list is handed over
// without the shared file, so aider does not read the digest twice.
func TestAiderReadArgsSwapsTheSharedFile(t *testing.T) {
	home, proj := aiderReadHome(t)
	conf := "read:\n  - CONVENTIONS.md\n  - " + aiderContextPath() + "\n"
	if err := os.WriteFile(aiderConfPath(), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(home, "live.md")
	want := []string{"--read", "CONVENTIONS.md", "--read", live}
	if got := aiderReadArgs(nil, proj, live); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAiderChatHistory(t *testing.T) {
	_, proj := aiderReadHome(t)
	if got := aiderChatHistory(nil, proj); got != filepath.Join(proj, ".aider.chat.history.md") {
		t.Errorf("default: %s", got)
	}
	if err := os.WriteFile(filepath.Join(proj, ".aider.conf.yml"), []byte("chat-history-file: logs/h.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := aiderChatHistory(nil, proj); got != filepath.Join(proj, "logs", "h.md") {
		t.Errorf("config: %s", got)
	}
	if got := aiderChatHistory([]string{"--chat-history-file", "c.md"}, proj); got != filepath.Join(proj, "c.md") {
		t.Errorf("command line: %s", got)
	}
}

func TestAiderLastAsk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.md")
	old := "# aider chat started at 2026-10-01\n\n#### asked last week  \n\nanswer\n"
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	off := int64(len(old))
	if got := aiderLastAsk(p, off); got != "" {
		t.Errorf("a message from before the run: %q", got)
	}
	add := func(s string) {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(s)
		_ = f.Close()
	}
	add("\n#### why does the cache  \n#### drop warm entries?  \n")
	if got := aiderLastAsk(p, off); got != "why does the cache\ndrop warm entries?" {
		t.Errorf("two-line message: %q", got)
	}
	add("\nreply\n\n#### /ask where is evict.go  \n")
	if got := aiderLastAsk(p, off); got != "where is evict.go" {
		t.Errorf("/ask: %q", got)
	}
	add("\n#### /add evict.go  \n")
	if got := aiderLastAsk(p, off); got != "" {
		t.Errorf("an aider command is not a question: %q", got)
	}
	add("\n#### /etc/hosts is wrong on the build box  \n")
	if got := aiderLastAsk(p, off); !strings.HasPrefix(got, "/etc/hosts") {
		t.Errorf("a path is the reader's words: %q", got)
	}
}

// The live file is a pipe deja answers on every read, with the recall for the
// message aider has just written to its history.
func TestServeAiderLiveAnswersEachRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFO")
	}
	n := 0
	path, stop, err := serveAiderLive(func() string { n++; return "answer " + string(rune('0'+n)) })
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"answer 1", "answer 2"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Errorf("read %d: %q, want %q", i, b, want)
		}
	}
	stop()
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("the pipe's directory outlived the run: %v", err)
	}
}
