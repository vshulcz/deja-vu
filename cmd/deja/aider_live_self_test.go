package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The aider `deja aider` started is a session in the index as soon as anything
// refreshes it, under the id the reader gives it. The live recall asked under
// an id of its own, so the session being written came back to itself as "this
// was asked here before", and the serving spent the project's cooldown the next
// aider needed it for. Measured on aider 0.86.2.
func TestALiveAiderIsNotRecalledToItself(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	t.Setenv("DEJA_AIDER_ROOTS", "")

	history := filepath.Join(home, ".aider.chat.history.md")
	appendTo := func(s string) {
		t.Helper()
		f, err := os.OpenFile(history, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-72 * time.Hour).Format("2006-01-02 15:04:05")
	appendTo("\n# aider chat started at " + old + "\n\n" +
		"#### the gateway_timeout in the reconnect_loop drops heartbeats  \n\n" +
		"The reconnect_loop gives up after gateway_timeout; raise the deadline.\n\n")

	dir := filepath.Join(tmp, "index.db")
	l := newAiderLive(dir, home, history, "")
	// What aider writes on start, and the first message of this run.
	appendTo("\n# aider chat started at " + time.Now().Format("2006-01-02 15:04:05") + "\n\n" +
		"#### the osprey_worker retries seven times before parking_jobs  \n\n" +
		"Noted: osprey_worker retries seven times, then parks the job.\n\n")
	// Any other harness's hook, or a search, refreshes the index meanwhile.
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := sources.ParseAiderFile(history)
	if err != nil || len(ss) != 2 {
		t.Fatalf("history parsed to %d sessions (%v)", len(ss), err)
	}
	own := ss[1].ID

	appendTo("#### how many times does the osprey_worker retry before parking_jobs  \n\n")
	if got := l.body(); strings.Contains(got, "osprey_worker") || strings.Contains(got, shortID(own)) {
		t.Errorf("the session being written was recalled to itself (%s):\n%s", own, got)
	}

	// Control: the same reader, asked about the session before it, gets it.
	appendTo("#### the gateway_timeout in the reconnect_loop is back  \n\n")
	if got := l.body(); !strings.Contains(got, "gateway_timeout") {
		t.Errorf("an earlier session was not recalled:\n%s", got)
	}
}
