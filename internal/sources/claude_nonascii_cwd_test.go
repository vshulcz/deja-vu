package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Claude Code names a project folder by writing every character outside
// [A-Za-z0-9] as "-", so a directory named in Cyrillic, CJK or with accents
// cannot be read back from the folder name. Every record carries the real
// directory as cwd (#4175).
func TestAClaudeSessionInANonASCIIDirectoryKeepsItsProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	work := filepath.Join(home, "w", "проект é")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	enc := claudeEncodePath(work)
	if strings.Count(enc, "-") < 9 {
		t.Fatalf("encoding looks wrong: %q", enc)
	}
	dir := filepath.Join(home, ".claude", "projects", enc)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "11111111-2222-4333-8444-555555555555.jsonl")
	line := `{"type":"user","sessionId":"11111111-2222-4333-8444-555555555555","cwd":"` + work + `","timestamp":"2026-09-20T10:00:00Z","uuid":"u1","message":{"role":"user","content":"кэш живёт в «ёлке»"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	projectNameCache.Delete(enc)
	ss, err := parseClaudeFileFromOffset(path, 0)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	if want := "w/проект é"; ss[0].Project != want {
		t.Errorf("project = %q, want %q", ss[0].Project, want)
	}
	if got := ClaudeSessionDir(path); got != work {
		t.Errorf("session dir = %q, want %q", got, work)
	}
}

// A cwd that is not the directory the folder was named for — the session
// moved somewhere else first — is not taken for it.
func TestAClaudeCWDThatIsNotTheFolderIsIgnored(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "-work-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","cwd":"/elsewhere","message":{"role":"user","content":"x"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeTranscriptCWD(path, "-work-app"); got != "" {
		t.Errorf("took %q for -work-app", got)
	}
}

// An empty segment is not a directory: "007---" named a child the walk could
// not spell, and closing it as "/" handed back the parent with slashes on.
func TestResolveEncodedPathNeverReturnsEmptySegments(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "007"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := claudeEncodePath(filepath.Join(root, "007")) + "---------"
	if got := ResolveEncodedPath(base); strings.Contains(got, string(filepath.Separator)+string(filepath.Separator)) || strings.HasSuffix(got, string(filepath.Separator)) {
		t.Errorf("resolved %q to %q", base, got)
	}
}
