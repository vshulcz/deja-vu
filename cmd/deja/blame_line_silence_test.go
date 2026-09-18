package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A line asked about and then not mentioned at all leaves the reader unable to
// tell "this line has no history" from "your `:120` was ignored". Found by
// running the edges: a line past the end of the file, a directory, a file
// outside a repository and a file that is not there all printed the ordinary
// listing and nothing about the line (#3726).
func TestLineBlameSaysWhyItCannotAnswer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "app")
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	tracked := filepath.Join(repo, "internal", "pool.go")
	if err := os.WriteFile(tracked, []byte("package app\n\nvar conns = 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", "internal/pool.go")
	git("commit", "-qm", "first")
	untracked := filepath.Join(repo, "internal", "new.go")
	if err := os.WriteFile(untracked, []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "notes.md")
	if err := os.WriteFile(outside, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		line int
		want string
	}{
		{"past the end of the file", tracked, 900, "the file has 3 lines"},
		{"a directory", filepath.Join(repo, "internal"), 12, "that is a directory"},
		{"a file that is not there", filepath.Join(repo, "internal", "nope.go"), 4, "there is no such file here"},
		{"outside a repository", outside, 1, "not in a repository"},
		{"not tracked", untracked, 1, "not tracked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, why := gitLineCommit(tc.path, tc.line)
			if why == "" {
				t.Fatalf("no reason given for %s", tc.name)
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", why, tc.want)
			}
		})
	}

	// And the line it can answer for carries no refusal.
	if c, why := gitLineCommit(tracked, 3); why != "" || c.SHA == "" {
		t.Fatalf("a committed line should resolve: sha=%q why=%q", c.SHA, why)
	}
}
