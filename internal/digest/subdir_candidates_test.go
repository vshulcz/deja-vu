package digest

import (
	"os"
	"os/exec"
	"path/filepath"
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
