package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func gitRepoWithCommit(t *testing.T) (root, file string, committed time.Time) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root = t.TempDir()
	file = filepath.Join(root, "app.go")
	if err := os.WriteFile(file, []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "app.go")
	run("commit", "-qm", "first")
	return root, file, time.Now()
}

func TestFilesMovedSinceCountsCommitsAfterTheSession(t *testing.T) {
	_, file, _ := gitRepoWithCommit(t)
	// The session ended a day before the commit, so the file moved under it.
	since := time.Now().Add(-24 * time.Hour)
	r := filesMovedSince([]string{file}, since, 5*time.Second)
	if r.Changed != 1 {
		t.Fatalf("changed=%d looked=%d, want one file changed", r.Changed, r.Looked)
	}
	if movedNote(r) != "  1 file this session touched has changed since" {
		t.Fatalf("note = %q", movedNote(r))
	}
}

func TestFilesMovedSinceStaysSilentOnFreshSessions(t *testing.T) {
	_, file, _ := gitRepoWithCommit(t)
	// Nothing can have been committed after a session that ended a minute ago,
	// and asking would cost a fork on the common search.
	r := filesMovedSince([]string{file}, time.Now().Add(-time.Minute), 5*time.Second)
	if r.Changed != 0 || r.Looked != 0 {
		t.Fatalf("%+v, want no work done at all", r)
	}
}

func TestFilesMovedSinceSkipsFilesOlderThanTheSession(t *testing.T) {
	_, file, _ := gitRepoWithCommit(t)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	// mtime before the session: it cannot have changed since, and the answer
	// costs a stat rather than a git invocation.
	r := filesMovedSince([]string{file}, time.Now().Add(-24*time.Hour), 5*time.Second)
	if r.Changed != 0 {
		t.Fatalf("changed=%d, want the mtime shortcut to answer", r.Changed)
	}
	if r.Looked != 1 {
		t.Fatalf("looked=%d, want the file counted as considered", r.Looked)
	}
}

func TestFilesMovedSinceIgnoresWhatItCannotResolve(t *testing.T) {
	// A path that no longer exists says nothing in either direction: it may
	// have been renamed, or the checkout may live elsewhere now.
	r := filesMovedSince([]string{filepath.Join(t.TempDir(), "gone.go")},
		time.Now().Add(-24*time.Hour), time.Second)
	if r.Changed != 0 || r.Looked != 0 {
		t.Fatalf("%+v, want silence", r)
	}
	if movedNote(r) != "" {
		t.Fatalf("note = %q, want nothing", movedNote(r))
	}
	if got := filesMovedSince(nil, time.Now().Add(-time.Hour), time.Second); got.Looked != 0 {
		t.Fatal("no paths, no work")
	}
	if got := filesMovedSince([]string{"/x"}, time.Time{}, time.Second); got.Looked != 0 {
		t.Fatal("a session with no end time cannot be compared against")
	}
}

func TestMovedNoteWording(t *testing.T) {
	if movedNote(movedReport{}) != "" {
		t.Fatal("nothing changed, nothing said")
	}
	if got := movedNote(movedReport{Changed: 4, Looked: 6}); got != "  4 files this session touched have changed since" {
		t.Fatalf("got %q", got)
	}
}

func TestGitRootOfWalksUpAndTerminates(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitRootOf(filepath.Join(deep, "x.go")); got != "" {
		t.Fatalf("no repository above it, got %q", got)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitRootOf(filepath.Join(deep, "x.go")); got != root {
		t.Fatalf("got %q, want %q", got, root)
	}
	// Terminates at the volume root rather than spinning there, which is the
	// Windows failure this shape of loop keeps producing.
	done := make(chan string, 1)
	go func() {
		done <- gitRootOf(filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "nowhere", "x.go"))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("gitRootOf did not terminate")
	}
}
