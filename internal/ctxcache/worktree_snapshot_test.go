package ctxcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// A repository does not need its first commit to be a valid workspace. Both
// its staged content and later edits must invalidate a previously read packet.
func TestWorktreeSnapshotsBeforeFirstCommit(t *testing.T) {
	repo := t.TempDir()
	worktreeFixtureGit(t, repo, "init")
	path := filepath.Join(repo, "tracked")
	worktreeFixtureWrite(t, path, "staged\n")
	worktreeFixtureGit(t, repo, "add", "tracked")
	worktreeFixtureWrite(t, path, "dirty one\n")
	a, err := ResolveIdentity(repo, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worktreeFixtureWrite(t, path, "dirty two\n")
	b, err := ResolveIdentity(repo, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if a.WorktreeState == b.WorktreeState {
		t.Fatal("unborn repository lost a second edit to a tracked dirty file")
	}
	worktreeFixtureGit(t, repo, "add", "tracked")
	c, err := ResolveIdentity(repo, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if b.WorktreeState == c.WorktreeState {
		t.Fatal("staging changed the index but not the freshness marker")
	}
}

func TestWorktreeSnapshotsTrackNestedUntrackedFilesAndSymlinks(t *testing.T) {
	repo := t.TempDir()
	worktreeFixtureGit(t, repo, "init")
	if err := os.Mkdir(filepath.Join(repo, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "nested", "new file")
	worktreeFixtureWrite(t, path, "one")
	a, err := ResolveIdentity(repo, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worktreeFixtureWrite(t, path, "two")
	b, err := ResolveIdentity(repo, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if a.WorktreeState == b.WorktreeState {
		t.Fatal("untracked directory summary hid a content change")
	}
	if runtime.GOOS == "windows" {
		return
	} // symbolic-link creation may require a privileged Windows account
	link := filepath.Join(repo, "link")
	if err := os.Symlink("missing-a", link); err != nil {
		t.Fatal(err)
	}
	c, err := ResolveIdentity(repo, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-b", link); err != nil {
		t.Fatal(err)
	}
	d, err := ResolveIdentity(repo, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if c.WorktreeState == d.WorktreeState {
		t.Fatal("untracked symlink target change did not invalidate context")
	}
}

func worktreeFixtureGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
}

func worktreeFixtureWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
