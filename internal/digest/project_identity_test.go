package digest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProjectNameCandidatesIncludesWorktreeSiblings(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	main := filepath.Join(tmp, "deja-vu")
	wt := filepath.Join(tmp, "deja-vu-fix")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", main}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(main, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "seed")
	run("worktree", "add", "-q", wt, "-b", "fix")

	names := ProjectNameCandidates(wt)
	var hasMainBase bool
	for _, n := range names {
		if n == "deja-vu" {
			hasMainBase = true
		}
	}
	if !hasMainBase {
		t.Fatalf("worktree lookup must include the main checkout's name, got %v", names)
	}
}

func TestProjectNameCandidatesPlainDirUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "utils")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := ProjectNameCandidates(dir)
	if len(names) == 0 || names[0] != "utils" {
		t.Fatalf("plain dir must keep the classic forms, got %v", names)
	}
}
