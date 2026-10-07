package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// aider has no hook on a user message. The stand-in does what aider 0.86.2
// does on each message: append it to the chat history, then read every
// read-only file. The recall for that message has to be in what it reads, and
// the project's own read: list has to survive.
func TestDejaAiderRecallsForTheMessageBeingSent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in aider is a shell script, and there is no FIFO")
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(tmp, "proj", "beta")
	bin := filepath.Join(tmp, "bin")
	for _, d := range []string{home, filepath.Join(claude, "beta"), filepath.Join(claude, "alpha"), proj, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("AIDER_READ", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claude, "beta", "hookfix.jsonl"), fmt.Sprintf(`{"type":"user","sessionId":"hookfix","timestamp":%q,`+
		`"message":{"role":"user","content":"gateway_timeout on the reconnect_loop keeps dropping heartbeats"}}`+"\n", at))
	write(filepath.Join(claude, "alpha", "f.jsonl"), fmt.Sprintf(`{"type":"user","sessionId":"filler","timestamp":%q,`+
		`"message":{"role":"user","content":"unrelated work about deployments and dashboards"}}`+"\n", at))
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := installAider("", false); err != nil {
		t.Fatal(err)
	}
	// The project's own list, which hid the home one.
	write(filepath.Join(proj, ".aider.conf.yml"), "read:\n  - CONVENTIONS.md\n")
	write(filepath.Join(proj, "CONVENTIONS.md"), "conventions\n")
	t.Chdir(proj)

	args, before, seen := filepath.Join(tmp, "args"), filepath.Join(tmp, "before"), filepath.Join(tmp, "seen")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + args + "'\n" +
		"for a; do last=$a; done\n" +
		"cat \"$last\" > '" + before + "'\n" +
		"printf '\\n#### seeing gateway_timeout in the reconnect_loop again  \\n' >> .aider.chat.history.md\n" +
		"cat \"$last\" > '" + seen + "'\n"
	write(filepath.Join(bin, "aider"), script)
	if err := os.Chmod(filepath.Join(bin, "aider"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := cmdAider(dir, []string{"--message", "hi"}, ""); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(args)
	if !strings.Contains(string(a), "--read\nCONVENTIONS.md\n--read\n") {
		t.Errorf("the project's read: list was not handed on:\n%s", a)
	}
	if strings.Contains(string(a), "aider-context.md") {
		t.Errorf("the shared context file is read beside the live one:\n%s", a)
	}
	if b, _ := os.ReadFile(before); strings.Contains(string(b), promptHookLead) {
		t.Fatalf("a recall before any message was sent:\n%s", b)
	}
	got, _ := os.ReadFile(seen)
	if !strings.Contains(string(got), promptHookLead) || !strings.Contains(string(got), "gateway_timeout") {
		t.Errorf("the message being sent got no recall:\n%s", got)
	}
}
