package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// aider logs every input as a "#### " line, commands included; a line that is
// only a slash command is not a question, and `/clear` was glued onto the
// question typed after it (#3248).
func TestParseAiderSkipsSlashCommands(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".aider.chat.history.md")
	body := "# aider chat started at 2026-09-08 01:00:00\n\n#### /add src/pager.go\n\n#### /undo\n\n#### /clear\n\n#### why does the pager stall on the last page\n\nbecause the cursor overflows\n\n#### /etc/hosts is wrong on the staging box, fix the entry\n\ndone\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	want := []string{"why does the pager stall on the last page", "/etc/hosts is wrong on the staging box, fix the entry"}
	if len(users) != len(want) || users[0] != want[0] || users[1] != want[1] {
		t.Fatalf("user lines = %q, want %q", users, want)
	}
}
