package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Two files under one directory printed as "tmp/b7/repo/x.go" and
// "b7/repo/internal/y.go", which read as relative paths starting in different
// places rather than as one tree with its head removed (#727).
func TestTrimPathMarksTheCut(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/b7/repo/retry.go", "tmp/b7/repo/retry.go"},
		{"/tmp/b7/repo/internal/backoff.go", "…/b7/repo/internal/backoff.go"},
		{"/a/b/c/d/e/f/g.go", "…/d/e/f/g.go"},
		{"retry.go", "retry.go"},
		{"repo/retry.go", "repo/retry.go"},
		// Four segments is the limit, and dropping the empty root of an
		// absolute path is not a truncation.
		{"/a/b/c/d.go", "a/b/c/d.go"},
		{"/a/b/c/d/e.go", "…/b/c/d/e.go"},
	}
	for _, c := range cases {
		if got := trimPath(c.in); got != c.want {
			t.Errorf("trimPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A filter the caller set is not the topic's fault: sessions can mention it and
// still be absent because they are in another project (#727).
func TestFilesEmptyAnswerNamesTheProjectFilter(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"s1","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the retry storm on checkout"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := runFiles(index.DefaultDir(), []string{"retry", "--project", "nosuch"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `in project "nosuch"`) {
		t.Errorf("filtered answer: %q", buf.String())
	}
	buf.Reset()
	if err := runFiles(index.DefaultDir(), []string{"nothing-like-this"}, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "in project") {
		t.Errorf("unfiltered answer mentions a project: %q", buf.String())
	}
}
