package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aider writes what it did as its own output — `> Applied edit to x`,
// `> Added x to the chat.` — and a shell command as a `#### /run` input. None of
// it reached the index, and a launch that only ran a command was dropped whole
// (#4324).
func TestAiderRecordsFilesAndCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aider.chat.history.md")
	const doc = "# aider chat started at 2026-09-30 10:00:00\n\n" +
		"> Added worker.py to the chat.  \n\n" +
		"#### fix the retry loop in worker.py  \n\n" +
		"worker.py\n```python\ndef retry(n):\n    pass\n```\n\n" +
		"> Applied edit to worker.py  \n\n" +
		"#### /add docs/notes.md  \n" +
		"> Added docs/notes.md to the chat  \n\n" +
		"#### /read-only CONVENTIONS.md  \n" +
		"> Added CONVENTIONS.md to read-only files.  \n\n" +
		"# aider chat started at 2026-09-30 10:05:00\n\n" +
		"#### /run pytest -q tests/test_worker.py  \n" +
		"> Add 0.1k tokens of command output to the chat? (Y)es/(N)o [Yes]: y  \n" +
		"> Added 3 lines of output to the chat.  \n\n" +
		"#### !go test ./...  \n\n" +
		"#### /git status  \n\n" +
		"#### /test make check  \n\n" +
		"> Running npm test  \n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 {
		t.Fatalf("sessions = %d, want 2 — the launch that only ran commands is a session", len(ss))
	}
	got := func(s int, role string) []string {
		var out []string
		for _, m := range ss[s].Messages {
			if m.Role == role {
				out = append(out, m.Text)
			}
		}
		return out
	}
	wantFiles := []string{
		filepath.Join(dir, "worker.py"),
		filepath.Join(dir, "docs", "notes.md"),
		filepath.Join(dir, "CONVENTIONS.md"),
	}
	if f := got(0, RoleFiles); strings.Join(f, "|") != strings.Join(wantFiles, "|") {
		t.Errorf("files = %q, want %q", f, wantFiles)
	}
	if c := got(1, RoleFiles); len(c) != 0 {
		t.Errorf("`Added 3 lines of output` is not a file: %q", c)
	}
	wantCmds := []string{"pytest -q tests/test_worker.py", "go test ./...", "git status", "make check", "npm test"}
	if c := got(1, RoleCommand); strings.Join(c, "|") != strings.Join(wantCmds, "|") {
		t.Errorf("commands = %q, want %q", c, wantCmds)
	}
	if u := got(1, "user"); len(u) != 0 {
		t.Errorf("a command is not a question: %q", u)
	}
}

// aider logs `/ask <q>` and then `<q>` again when the sub-coder takes it, so
// keeping the command line indexed the question twice and titled the session
// with the command (#4325).
func TestAiderAskIsIndexedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".aider.chat.history.md")
	const doc = "# aider chat started at 2026-09-30 10:10:00\n\n" +
		"#### /ask what does worker.py do?  \n\n" +
		"#### what does worker.py do?  \n\n" +
		"It retries n times with a one second sleep.\n\n" +
		"#### /code add jitter  \n\n" +
		"#### add jitter  \n\n" +
		"Done.\n\n" +
		"#### /context where is the backoff  \n\n" +
		"#### where is the backoff  \n\n" +
		"In worker.py.\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	want := []string{"what does worker.py do?", "add jitter", "where is the backoff"}
	if strings.Join(users, "|") != strings.Join(want, "|") {
		t.Fatalf("user turns = %q, want %q", users, want)
	}
}
