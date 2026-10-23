package main

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/gitread"
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
// loaded windows leg). Each round now times seven bare git processes, the
// count the check makes, right before the check itself. Load slows both alike,
// so the middle ratio of five rounds holds where the wall time does not. A
// check costing more than 1.6 times those seven processes fails: about four
// more git calls, or a stall of half what seven processes take.
func TestCompactionFreshnessBudgetIsSizedForTheSlowPlatform(t *testing.T) {
	repo := compactionGitRepo(t)
	compactionWrite(t, filepath.Join(repo, "tracked.txt"), "base\n")
	compactionGit(t, repo, "add", "tracked.txt")
	compactionGit(t, repo, "commit", "-m", "base")
	compactionWrite(t, filepath.Join(repo, "tracked.txt"), "dirty\n")

	// Idle or loaded the middle ratio sits at 0.9-1.3 on a mac; four more
	// calls put it at 1.6-1.9 and twice the calls at 2.0-2.4.
	const gitCalls, rounds, batchesAllowed, maxRatio = 7, 5, 3, 1.6
	sevenBare := func() time.Duration {
		start := time.Now()
		for range gitCalls {
			if out, err := gitread.Command(context.Background(), "-C", repo, "rev-parse", "--is-inside-work-tree").Output(); err != nil {
				t.Fatalf("bare git: %v: %s", err, out)
			}
		}
		return time.Since(start)
	}
	sevenBare() // the first git of a run pays for a cold binary
	best := time.Duration(1 << 62)
	// batch returns the sorted ratios of five rounds. A burst of load can still
	// land on most of one batch, so a batch over the line gets two more
	// chances; a check that really does more work is over in every one.
	batch := func() []float64 {
		ratios := make([]float64, 0, rounds)
		var partial []time.Duration
		for range rounds {
			bare := sevenBare()
			start := time.Now()
			freshness := compactionFreshness(repo)
			took := time.Since(start)
			if freshness.Error != "" || strings.HasPrefix(freshness.WorktreeState, "partial:") {
				// One round past the budget is the runner; most of them is
				// the check (#3645).
				partial = append(partial, took.Round(time.Millisecond))
				continue
			}
			best = min(best, took)
			ratios = append(ratios, float64(took)/float64(bare))
		}
		if len(ratios) <= rounds/2 {
			t.Fatalf("a repository with one dirty file did not fingerprint in %v in %d of %d rounds (they took %v)",
				compactionGitBudget, rounds-len(ratios), rounds, partial)
		}
		slices.Sort(ratios)
		return ratios
	}
	var batches [][]float64
	for len(batches) < batchesAllowed {
		ratios := batch()
		batches = append(batches, ratios)
		if ratios[len(ratios)/2] <= maxRatio {
			break
		}
	}
	last := batches[len(batches)-1]
	ratio := last[len(last)/2]
	t.Logf("freshness at best %v of a %v budget; against %d bare git processes run just before it, ratios by batch %.2f",
		best.Round(time.Millisecond), compactionGitBudget, gitCalls, batches)
	// Half the budget spent on the smallest repository there is means the
	// margin is gone for a real one.
	if best > compactionGitBudget/2 {
		t.Errorf("one dirty file cost at best %v of the %v budget, leaving no margin for a real repository",
			best.Round(time.Millisecond), compactionGitBudget)
	}
	if ratio > maxRatio {
		t.Errorf("one dirty file cost %.2f times what %d bare git processes cost just before it in each of %d batches (%.2f); the check makes %d calls and may cost %.1f times",
			ratio, gitCalls, len(batches), batches, gitCalls, maxRatio)
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
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
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
