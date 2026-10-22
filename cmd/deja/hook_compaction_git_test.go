package main

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/gitread"
	"github.com/vshulcz/deja-vu/internal/model"
)

func TestCompactionFreshnessTracksDirtyStagedAndUntrackedChanges(t *testing.T) {
	repo := compactionGitRepo(t)
	tracked := filepath.Join(repo, "tracked.txt")
	compactionWrite(t, tracked, "base\n")
	compactionGit(t, repo, "add", "tracked.txt")
	compactionGit(t, repo, "commit", "-m", "base")

	compactionWrite(t, tracked, "dirty one\n")
	dirtyOne := compactionFreshness(repo)
	compactionWrite(t, tracked, "dirty two\n")
	dirtyTwo := compactionFreshness(repo)
	if dirtyOne.Error != "" || dirtyTwo.Error != "" || dirtyOne.WorktreeState == dirtyTwo.WorktreeState {
		t.Fatalf("dirty edit was not fingerprinted: first=%#v second=%#v", dirtyOne, dirtyTwo)
	}

	compactionGit(t, repo, "add", "tracked.txt")
	staged := compactionFreshness(repo)
	if staged.Error != "" || staged.WorktreeState == dirtyTwo.WorktreeState {
		t.Fatalf("staging was not fingerprinted: dirty=%#v staged=%#v", dirtyTwo, staged)
	}

	untracked := filepath.Join(repo, "nested", "note.txt")
	compactionWrite(t, untracked, "one\n")
	firstUntracked := compactionFreshness(repo)
	compactionWrite(t, untracked, "two\n")
	secondUntracked := compactionFreshness(repo)
	if firstUntracked.Error != "" || secondUntracked.Error != "" || firstUntracked.WorktreeState == secondUntracked.WorktreeState {
		t.Fatalf("untracked edit was not fingerprinted: first=%#v second=%#v", firstUntracked, secondUntracked)
	}
}

func TestCompactionFreshnessMarksLargeUntrackedFilesPartial(t *testing.T) {
	repo := compactionGitRepo(t)
	artifact := filepath.Join(repo, "generated-artifact")
	f, err := os.Create(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxUntrackedFingerprintFileBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	freshness := compactionFreshness(repo)
	if !strings.HasPrefix(freshness.WorktreeState, "partial:") {
		t.Fatalf("large artifact was treated as complete: %#v", freshness)
	}
	if !strings.Contains(freshness.Error, "partial") {
		t.Fatalf("partial fingerprint did not require validation: %#v", freshness)
	}
}

// The budget is a ceiling against a hung git, not a performance claim, and a
// fingerprint that runs out of it is worse than a slow one: it comes back
// `partial:` and tells the agent not to trust the context it was just given.
// Seven git calls share it, so it has to be sized for the platform where a
// process is expensive — the windows runner spent all 750 ms of the old one on
// a repository with one dirty file (#3645).
//
// A wall-clock reading on a shared runner is mostly the runner's load, so one
// sample against a fixed share of the budget flaked (#5551: 2.4 s of 4 s on a
// loaded windows leg). The check is timed against one bare git process on the
// same runner, and the best complete round of a few is kept, so a load spike
// has to hit every round to fail it while a hook that starts doing more work
// still does.
func TestCompactionFreshnessBudgetIsSizedForTheSlowPlatform(t *testing.T) {
	repo := compactionGitRepo(t)
	compactionWrite(t, filepath.Join(repo, "tracked.txt"), "base\n")
	compactionGit(t, repo, "add", "tracked.txt")
	compactionGit(t, repo, "commit", "-m", "base")
	compactionWrite(t, filepath.Join(repo, "tracked.txt"), "dirty\n")

	// compactionFreshness makes seven git calls; a few more process costs of
	// slack cover status and diff doing real work, a regression does not fit.
	const gitCalls, slack = 7, 5
	const rounds = 5
	best, bare := time.Duration(1<<62), time.Duration(1<<62)
	var last model.RepositoryFreshness
	bareGit := func() {
		start := time.Now()
		if out, err := gitread.Command(context.Background(), "-C", repo, "rev-parse", "--is-inside-work-tree").Output(); err != nil {
			t.Fatalf("bare git: %v: %s", err, out)
		}
		bare = min(bare, time.Since(start))
	}
	// A high reading of the baseline would loosen the check, so take its best
	// before the first round can pass on it.
	for range rounds {
		bareGit()
	}
	for range rounds {
		bareGit()
		start := time.Now()
		last = compactionFreshness(repo)
		took := time.Since(start)
		if last.Error != "" || strings.HasPrefix(last.WorktreeState, "partial:") {
			continue
		}
		best = min(best, took)
		if best <= compactionGitBudget/2 && best <= (gitCalls+slack)*bare {
			break
		}
	}
	if best == time.Duration(1<<62) {
		t.Fatalf("a repository with one dirty file did not fingerprint in %v in any of %d rounds: %#v",
			compactionGitBudget, rounds, last)
	}
	t.Logf("best of up to %d rounds: freshness %v, one bare git %v, budget %v",
		rounds, best.Round(time.Millisecond), bare.Round(time.Millisecond), compactionGitBudget)
	// Half the budget spent on the smallest repository there is means the
	// margin is gone for a real one.
	if best > compactionGitBudget/2 {
		t.Errorf("one dirty file cost at best %v of the %v budget, leaving no margin for a real repository",
			best.Round(time.Millisecond), compactionGitBudget)
	}
	if limit := (gitCalls + slack) * bare; best > limit {
		t.Errorf("one dirty file cost at best %v, %.1f bare git processes (%v each); the check makes %d calls and may cost %d",
			best.Round(time.Millisecond), float64(best)/float64(bare), bare.Round(time.Millisecond), gitCalls, gitCalls+slack)
	}
	if win, unix := compactionBudgetFor("windows"), compactionBudgetFor("linux"); win <= unix {
		t.Errorf("windows budget = %v against %v elsewhere: the platform where a process is expensive needs more, not the same",
			win, unix)
	}
	if compactionBudgetFor(runtime.GOOS) != compactionGitBudget {
		t.Errorf("the budget in use (%v) is not the one this platform gets (%v)",
			compactionGitBudget, compactionBudgetFor(runtime.GOOS))
	}
}

func TestCompactionFreshnessNamesRepositoryErrors(t *testing.T) {
	freshness := compactionFreshness(filepath.Join(t.TempDir(), "not-a-repository"))
	if freshness.Error == "" || freshness.WorktreeState != "" {
		t.Fatalf("non-repository freshness implied usable state: %#v", freshness)
	}
}

func TestFingerprintStreamsFailClosedAfterCancellation(t *testing.T) {
	repo := compactionGitRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := sha256.New()
	if hashGitStream(ctx, h, repo, "status", "--porcelain=v1") {
		t.Fatal("cancelled git stream was accepted")
	}
	if !hashUntrackedStream(ctx, h, repo) {
		t.Fatal("cancelled untracked stream was treated as complete")
	}
}

func compactionGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	compactionGit(t, repo, "init")
	compactionGit(t, repo, "config", "user.email", "fixture@example.invalid")
	compactionGit(t, repo, "config", "user.name", "fixture")
	return repo
}

func compactionGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func compactionWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
