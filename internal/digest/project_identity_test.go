package digest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
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
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", main}, args...)...)
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

// Under an alias the alias comes first, then the names the directory had
// without it, worktree roots included: sessions indexed before the file existed
// carry those until a rebuild, and they went missing from recall (#5373).
func TestProjectNameCandidatesKeepTheOldNamesAfterTheAlias(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	main := filepath.Join(tmp, "deja-vu")
	trees := filepath.Join(tmp, "trees")
	wt := filepath.Join(trees, "deja-vu-fix")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", main}, args...)...)
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
	if err := os.MkdirAll(filepath.Dir(sources.ProjectAliasPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sources.ProjectAliasPath(), []byte(trees+" = work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(sources.ProjectAliasPath()) })

	names := ProjectNameCandidates(wt)
	if len(names) == 0 || names[0] != "work" {
		t.Fatalf("the alias must come first, got %v", names)
	}
	for _, want := range []string{"trees/deja-vu-fix", "deja-vu"} {
		if !slices.Contains(names, want) {
			t.Errorf("candidates %v lack %q", names, want)
		}
	}
	if got := PrimaryProjectName(wt); got != "work" {
		t.Errorf("PrimaryProjectName = %q, want work", got)
	}
	// Control: the main checkout is outside the tree and keeps its name first;
	// its worktree's alias joins after it.
	if names := ProjectNameCandidates(main); names[0] == "work" {
		t.Errorf("the main checkout outside the tree took the alias: %v", names)
	}
}
