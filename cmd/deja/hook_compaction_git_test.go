package main

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
