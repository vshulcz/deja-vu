package digest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An agent started in a package directory is standing in the project all the
// same, and the project is what recall is scoped by. The worktree roots are
// what carry that — except a repository with one worktree dropped its root,
// which is the only name a subdirectory does not already have (#2037).
func TestProjectNameCandidatesNameTheRepositoryFromASubdirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "work", "repo")
	sub := filepath.Join(repo, "cmd", "deja")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	// The premise: standing at the root, the project's own names are there.
	atRoot := ProjectNameCandidates(repo)
	if !has(atRoot, "work/repo") {
		t.Fatalf("the root does not name its own project: %v", atRoot)
	}

	got := ProjectNameCandidates(sub)
	if !has(got, "work/repo") {
		t.Errorf("a subdirectory of the repository does not name the project it is in: %v", got)
	}
	// And its own names stay: a session recorded against the subdirectory
	// before it was re-projected is still that session's.
	if !has(got, "cmd/deja") {
		t.Errorf("the subdirectory lost its own name: %v", got)
	}
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// Inside a submodule git names the gitdir rather than the working tree, so the
// root it reports is ".git/modules/<name>" — a path nobody worked in. Dropping
// the single root used to hide that; keeping it made the junk a candidate.
func TestProjectNameCandidatesSkipTheSubmoduleGitdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "work", "parent")
	child := filepath.Join(tmp, "work", "child")
	for _, d := range []string{parent, child} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
			if out, err := exec.Command("git", append([]string{"-C", d}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(child, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "f.txt"}, {"commit", "-q", "-m", "seed"}} {
		if out, err := exec.Command("git", append([]string{"-C", child}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	add := exec.Command("git", "-C", parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", child, "sub")
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("submodules unavailable here: %v %s", err, out)
	}

	got := ProjectNameCandidates(filepath.Join(parent, "sub"))
	for _, n := range got {
		if strings.Contains(n, "modules/") {
			t.Errorf("the gitdir reached the candidates as a project: %v", got)
		}
	}
	// The submodule is still a place someone works in, under its own name.
	if !has(got, "parent/sub") {
		t.Errorf("the submodule lost its own name: %v", got)
	}
}
