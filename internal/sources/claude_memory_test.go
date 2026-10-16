package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// Claude files a project's memory under the path with every non-alphanumeric
// character turned into a dash; a dot in a directory name is one of them.
func TestClaudeEncodeProject(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/x/coding/deja-vu": "-Users-x-coding-deja-vu",
		"/Users/x/.cache/wt46":    "-Users-x--cache-wt46",
		"/srv/app_v2":             "-srv-app-v2",
	} {
		if got := ClaudeEncodeProject(in); got != want {
			t.Errorf("ClaudeEncodeProject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClaudeMemoriesReadsFrontmatter(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir := filepath.Join(root, ClaudeEncodeProject("/w/app"), "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a.md":      "---\nname: a\ndescription: \"never add a \\\"Co-Authored-By\\\" trailer\"\nmetadata:\n  type: feedback\n  originSessionId: s-1\n  modified: 2026-10-01T10:00:00Z\n---\n\nThe **why** line.\n",
		"b.md":      "---\r\nname: b\r\ndescription: 'it''s pg-two'\r\ntype: project\r\nmodified: 2026-10-05T10:00:00Z\r\n---\r\nbody b\r\n",
		"MEMORY.md": "- [a](a.md)\n",
		"notes.txt": "not a memory",
	}
	for n, s := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ms := ClaudeMemories("/w/app", "/w/app")
	if len(ms) != 2 {
		t.Fatalf("got %d memories, want 2: %+v", len(ms), ms)
	}
	if ms[0].Name != "b" || ms[0].Description != "it's pg-two" || ms[0].Type != "project" || ms[0].Body != "body b" {
		t.Errorf("newest first, single-quoted, top-level type, CRLF: %+v", ms[0])
	}
	if ms[1].Description != `never add a "Co-Authored-By" trailer` || ms[1].Type != "feedback" || ms[1].Origin != "s-1" {
		t.Errorf("double-quoted with escapes, nested metadata: %+v", ms[1])
	}
	if got := ClaudeMemories("/w/other"); len(got) != 0 {
		t.Errorf("another project read this one's memory: %+v", got)
	}
}
