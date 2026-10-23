package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// partialClone makes a blob-less clone of a two-commit repository whose second
// commit renames and edits a file, so rename detection needs the old blob the
// clone does not have. The origin is a file:// URL: a lazy fetch from it
// writes a new pack, which is what the tests count, and nothing leaves the
// machine.
func partialClone(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	src := filepath.Join(base, "src")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+base,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	git(src, "init", "-q")
	var body strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&body, "line %d of a file long enough for inexact rename detection\n", i)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	git(src, "add", ".")
	git(src, "commit", "-q", "-m", "one")
	git(src, "mv", "a.txt", "b.txt")
	if err := os.WriteFile(filepath.Join(src, "b.txt"), []byte(body.String()+"extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(src, "add", ".")
	git(src, "commit", "-q", "-m", "two")
	git(src, "config", "uploadpack.allowFilter", "true")
	clone := filepath.Join(base, "clone")
	u := filepath.ToSlash(src)
	if !strings.HasPrefix(u, "/") {
		u = "/" + u // C:/… on Windows
	}
	git(base, "clone", "-q", "--filter=blob:none", "file://"+u, clone)
	return clone
}

func packCount(t *testing.T, clone string) int {
	t.Helper()
	packs, err := filepath.Glob(filepath.Join(clone, ".git", "objects", "pack", "*.pack"))
	if err != nil {
		t.Fatal(err)
	}
	return len(packs)
}

// #5053: the session-start digest's `git log --name-only` fetched the missing
// blobs from origin in a partial clone. The hook must answer from what is on
// disk and still name the files.
func TestChangedTaskFilesDoesNotLazyFetchInPartialClone(t *testing.T) {
	old := taskGitBudget
	taskGitBudget = 20 * time.Second
	t.Cleanup(func() { taskGitBudget = old })

	clone := partialClone(t)
	before := packCount(t, clone)
	got := changedTaskFiles(clone)
	if after := packCount(t, clone); after != before {
		t.Fatalf("changedTaskFiles fetched from the promisor remote: %d packs -> %d", before, after)
	}
	if !strings.Contains(strings.Join(got, ","), "b.txt") {
		t.Fatalf("changedTaskFiles = %v, want b.txt from the last commit", got)
	}
}

// `deja blame file:line` runs blame and show, which read blobs too.
func TestBlameGitDoesNotLazyFetchInPartialClone(t *testing.T) {
	clone := partialClone(t)
	before := packCount(t, clone)
	gitLineCommit(filepath.Join(clone, "b.txt"), 3)
	_, _ = gitRun(clone, "show", "--unified=0", "--format=", "HEAD")
	if after := packCount(t, clone); after != before {
		t.Fatalf("blame fetched from the promisor remote: %d packs -> %d", before, after)
	}
}

// Every git call deja makes goes through gitread, so the no-fetch and no-lock
// environment cannot be forgotten at a new call site. docsdate is left out: it
// runs from the site generators, never from deja.
func TestGitCallsGoThroughGitread(t *testing.T) {
	direct := regexp.MustCompile(`exec\.Command(Context)?\((ctx, )?"git"`)
	var bad []string
	for _, root := range []string{".", "../../internal"} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				switch filepath.Base(path) {
				case "gitread", "docsdate", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path) //nolint:gosec // the test walks the repo checkout
			if err != nil {
				return err
			}
			for i, ln := range strings.Split(string(b), "\n") {
				if direct.MatchString(ln) {
					bad = append(bad, fmt.Sprintf("%s:%d", path, i+1))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("git run without gitread.Command:\n%s", strings.Join(bad, "\n"))
	}
}
