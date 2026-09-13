package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The paths a parser reads are written by someone else's machine, and the
// fixtures in this repository all look like this one: ASCII, no spaces, no
// drive letters. That is how a Copilot Chat resource went into the files record
// percent-encoded and nobody here could see it — every path on this machine
// decodes to itself (#3498, #3505).
//
// So one hostile path, through the parsers that record what a turn touched. Not
// every harness: the ones whose transcripts are JSONL deja writes fixtures for,
// which is where a path arrives as a field rather than as a URI.
func TestAFilesRecordKeepsAHostilePathIntact(t *testing.T) {
	// A space, a non-ASCII character, a character that is a shell metacharacter
	// elsewhere, and a windows drive letter — the four shapes that have broken
	// something in a path before.
	for _, want := range []string{
		"/Users/me/my app/main.go",
		"/Users/me/проект/счёт.go",
		"/Users/me/a(b)/c d/e.go",
		"/c:/Users/me/my app/main.go",
	} {
		t.Run(want, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("USERPROFILE", filepath.Join(root, "home"))
			t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
			dir := filepath.Join(root, "claude", "-work-app")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			// The shape Claude Code writes an edit in: the path is a field of
			// the tool call, so whatever is in it has to come back unchanged.
			line := `{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","cwd":"/work/app",` +
				`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":` +
				`{"file_path":` + jsonString(want) + `,"old_string":"a","new_string":"b"}}]}}` + "\n"
			path := filepath.Join(dir, "s1.jsonl")
			if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
				t.Fatal(err)
			}
			ss, err := ParseClaudeFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(ss) != 1 {
				t.Fatalf("sessions = %d, want the one transcript", len(ss))
			}
			var got []string
			for _, m := range ss[0].Messages {
				if m.Role == RoleFiles || m.Role == RoleEdit {
					// An edit record is the path on its first line.
					got = append(got, strings.SplitN(m.Text, "\n", 2)[0])
				}
			}
			if len(got) == 0 {
				t.Fatalf("no record names the file at all: %#v", ss[0].Messages)
			}
			for _, p := range got {
				if p != want {
					t.Errorf("record has %q, want %q", p, want)
				}
			}
		})
	}
}

// jsonString quotes a path the way a transcript does, so the fixture is written
// by the same rules the parser reads it by.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
