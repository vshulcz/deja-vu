package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const aiderFixture = `# aider chat started at 2026-07-16 10:32:01

> /usr/local/bin/aider --model sonnet
> Aider v0.86.1

#### fix the off-by-one in pager.go
#### keep the tests green

I'll fix the loop bound:

` + "```go" + `
for i := 0; i < n; i++ {
#### this is code, not a user line
> and this is not tool output
` + "```" + `

> Applied edit to pager.go
> Commit a1b2c3d fix: off-by-one

#### <blank>

# aider chat started at 2026-07-16 11:00:00

#### second session question

Second session answer here.
`

func TestParseAiderFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".aider.chat.history.md")
	if err := os.WriteFile(p, []byte(aiderFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 {
		t.Fatalf("sessions = %d, want 2", len(ss))
	}
	s1 := ss[0]
	if s1.Harness != "aider" || s1.Started.Hour() != 10 {
		t.Fatalf("bad session meta: %#v", s1)
	}
	if len(s1.Messages) != 2 {
		t.Fatalf("s1 messages = %d, want user+assistant: %#v", len(s1.Messages), s1.Messages)
	}
	if s1.Messages[0].Role != "user" || s1.Messages[0].Text != "fix the off-by-one in pager.go\nkeep the tests green" {
		t.Fatalf("user message wrong: %#v", s1.Messages[0])
	}
	a := s1.Messages[1]
	if a.Role != "assistant" {
		t.Fatalf("assistant role wrong: %#v", a)
	}
	// fence content preserved verbatim, prefixes inside not treated as boundaries
	for _, want := range []string{"loop bound", "#### this is code", "> and this is not tool output"} {
		if !strings.Contains(a.Text, want) {
			t.Fatalf("assistant text missing %q: %q", want, a.Text)
		}
	}
	s2 := ss[1]
	if len(s2.Messages) != 2 || s2.Messages[0].Text != "second session question" || s2.Messages[1].Text != "Second session answer here." {
		t.Fatalf("s2 wrong: %#v", s2.Messages)
	}
	if s1.ID == s2.ID || s1.ID == "" {
		t.Fatalf("session ids not distinct: %q %q", s1.ID, s2.ID)
	}
}

func TestAiderFilesDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	roots := t.TempDir()
	proj := filepath.Join(roots, "group", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(home, ".aider.chat.history.md"),
		filepath.Join(roots, ".aider.chat.history.md"),
		filepath.Join(proj, ".aider.chat.history.md"),
	} {
		if err := os.WriteFile(p, []byte("# aider chat started at 2026-01-01 00:00:00\n#### q\nans\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_AIDER_ROOTS", roots)
	files := AiderFiles()
	if len(files) != 3 {
		t.Fatalf("discovered %d files, want 3: %v", len(files), files)
	}
}
