package ctxcache

import (
	"crypto/sha256"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestBoundedWorktreeDigestSupportsNonGitWorkspace(t *testing.T) {
	workspace := t.TempDir()
	first := boundedWorktreeDigest(workspace)
	second := boundedWorktreeDigest(workspace)
	if first == "" || strings.HasPrefix(first, "partial:") {
		t.Fatalf("non-git digest = %q", first)
	}
	if first != second {
		t.Fatalf("non-git digest changed: %q != %q", first, second)
	}
}

func TestBoundedWorktreeDigestTracksSmallUntrackedContent(t *testing.T) {
	repo := t.TempDir()
	worktreeFixtureGit(t, repo, "init")
	path := filepath.Join(repo, "nested", "note")
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	worktreeFixtureWrite(t, path, "one")
	first := boundedWorktreeDigest(repo)
	worktreeFixtureWrite(t, path, "two")
	second := boundedWorktreeDigest(repo)
	if strings.HasPrefix(first, "partial:") || strings.HasPrefix(second, "partial:") {
		t.Fatalf("small untracked files must be complete: %q, %q", first, second)
	}
	if first == second {
		t.Fatal("small untracked content change did not affect digest")
	}
}

func TestBoundedWorktreeDigestMarksOversizeFilePartial(t *testing.T) {
	repo := t.TempDir()
	worktreeFixtureGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "artifact"), make([]byte, maxUntrackedFingerprintFileBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if got := boundedWorktreeDigest(repo); !strings.HasPrefix(got, "partial:") {
		t.Fatalf("oversize untracked artifact was treated as complete: %q", got)
	}
}

func TestBoundedWorktreeDigestMarksTotalContentLimitPartial(t *testing.T) {
	repo := t.TempDir()
	worktreeFixtureGit(t, repo, "init")
	for i := 0; i < maxUntrackedFingerprintBytes/maxUntrackedFingerprintFileBytes+1; i++ {
		path := filepath.Join(repo, "artifact-"+strconv.Itoa(i))
		if err := os.WriteFile(path, make([]byte, maxUntrackedFingerprintFileBytes), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := boundedWorktreeDigest(repo); !strings.HasPrefix(got, "partial:") {
		t.Fatalf("aggregate untracked artifact limit was treated as complete: %q", got)
	}
}

func TestBoundedWorktreeDigestMarksEntryLimitPartial(t *testing.T) {
	repo := t.TempDir()
	worktreeFixtureGit(t, repo, "init")
	for i := 0; i <= maxUntrackedFingerprintFiles; i++ {
		path := filepath.Join(repo, "entry-"+strconv.Itoa(i))
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := boundedWorktreeDigest(repo); !strings.HasPrefix(got, "partial:") {
		t.Fatalf("untracked entry limit was treated as complete: %q", got)
	}
}

func TestPartialWorktreeFingerprintRequiresValidationUntilArtifactRemoved(t *testing.T) {
	repo := t.TempDir()
	worktreeFixtureGit(t, repo, "init")
	artifact := filepath.Join(repo, "generated-artifact")
	f, err := os.Create(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(50 << 20); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	partialID, err := ResolveIdentity(repo, "bounded-worktree")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(partialID.WorktreeState, "partial:") {
		t.Fatalf("oversize sparse artifact must produce a partial fingerprint: %q", partialID.WorktreeState)
	}

	root := t.TempDir()
	callerGap := Gap{
		Subject: "incomplete worktree fingerprint", Severity: "required", Source: "caller://validation",
		Reason: "the caller has an independent validation requirement",
	}
	checkpoint, err := Checkpoint(root, partialID, State{Objective: "verify artifact safety", Gaps: []Gap{callerGap}})
	if err != nil {
		t.Fatal(err)
	}
	if !hasWorktreeFingerprintGap(checkpoint.State.Gaps, "git://local/fingerprint") {
		t.Fatalf("checkpoint omitted required partial-fingerprint gap: %#v", checkpoint.State.Gaps)
	}
	if !hasWorktreeFingerprintGap(checkpoint.State.Gaps, callerGap.Source) {
		t.Fatalf("checkpoint dropped caller-owned same-subject gap: %#v", checkpoint.State.Gaps)
	}

	resume, err := Resume(root, partialID, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if resume.CacheStatus != "stale" || !hasWorktreeFingerprintGap(resume.Snapshot.State.Gaps, "git://local/fingerprint") {
		t.Fatalf("partial fingerprint must never resume as a hit: %#v", resume)
	}
	status, err := Inspect(root, partialID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "stale" || !contains(status.Changed, "worktree") {
		t.Fatalf("partial fingerprint inspection=%#v", status)
	}

	refreshed, err := RefreshDetailed(root, partialID)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(refreshed.DetectedChanges, "worktree") || !hasWorktreeFingerprintGap(refreshed.Snapshot.State.Gaps, "git://local/fingerprint") {
		t.Fatalf("refresh lost partial worktree validation: %#v", refreshed)
	}
	status, err = Inspect(root, partialID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "stale" || !contains(status.Changed, "worktree") {
		t.Fatalf("partial fingerprint inspection after refresh=%#v", status)
	}

	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	completeID, err := ResolveIdentity(repo, "bounded-worktree")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(completeID.WorktreeState, "partial:") {
		t.Fatalf("artifact removal must restore a complete fingerprint: %q", completeID.WorktreeState)
	}
	recovered, err := Checkpoint(root, completeID, refreshed.Snapshot.State)
	if err != nil {
		t.Fatal(err)
	}
	if hasWorktreeFingerprintGap(recovered.State.Gaps, "git://local/fingerprint") {
		t.Fatalf("checkpoint retained its resolved partial-fingerprint gap: %#v", recovered.State.Gaps)
	}
	if !hasWorktreeFingerprintGap(recovered.State.Gaps, callerGap.Source) {
		t.Fatalf("checkpoint removed caller-owned same-subject gap: %#v", recovered.State.Gaps)
	}
	resume, err = Resume(root, completeID, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if resume.CacheStatus != "hit" {
		t.Fatalf("complete fingerprint after recovery resumed as %q", resume.CacheStatus)
	}
}

func hasWorktreeFingerprintGap(gaps []Gap, source string) bool {
	for _, gap := range gaps {
		if gap.Subject == "incomplete worktree fingerprint" && gap.Source == source {
			return true
		}
	}
	return false
}

func TestWorktreeFingerprintHelpersFailClosed(t *testing.T) {
	root := t.TempDir()
	h := sha256.New()
	used := int64(0)
	if hashGitStream(h, filepath.Join(root, "missing"), "status") {
		t.Fatal("Git stream from a missing root was accepted")
	}
	if !hashUntrackedStream(h, filepath.Join(root, "missing")) {
		t.Fatal("untracked stream from a missing root was accepted")
	}
	for _, name := range []string{"", "../outside", "directory/../outside", filepath.Join(string(filepath.Separator), "outside")} {
		if safeGitRelativePath(name) {
			t.Fatalf("unsafe Git path accepted: %q", name)
		}
		if hashUntrackedFile(h, root, name, &used) {
			t.Fatalf("unsafe Git path was fingerprinted: %q", name)
		}
	}
	if !safeGitRelativePath("nested/file") {
		t.Fatal("valid Git-relative path rejected")
	}
	if hashUntrackedFile(h, root, "missing", &used) {
		t.Fatal("missing untracked file was fingerprinted")
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if hashUntrackedFile(h, root, "directory", &used) {
		t.Fatal("special untracked directory was fingerprinted")
	}
	if err := os.WriteFile(filepath.Join(root, "large"), make([]byte, maxUntrackedFingerprintFileBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if hashUntrackedFile(h, root, "large", &used) {
		t.Fatal("oversize untracked file was fingerprinted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if !hashUntrackedFile(h, root, "link", &used) {
		t.Fatal("untracked symlink was not fingerprinted")
	}
}

func TestWorktreeFingerprintRejectsFilesChangedDuringScan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "changing")
	worktreeFixtureWrite(t, path, "original")

	beforeOpen := &mutatingFingerprintHash{
		Hash:    sha256.New(),
		trigger: "content-size",
		mutate: func() {
			worktreeFixtureWrite(t, path, "replacement")
		},
	}
	used := int64(0)
	if hashUntrackedFile(beforeOpen, root, "changing", &used) {
		t.Fatal("file replaced between lstat and open was accepted")
	}

	worktreeFixtureWrite(t, path, "content")
	duringRead := &mutatingFingerprintHash{
		Hash:    sha256.New(),
		trigger: "content",
		mutate: func() {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString("+"); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		},
	}
	used = 0
	if hashUntrackedFile(duringRead, root, "changing", &used) {
		t.Fatal("file changed during content read was accepted")
	}
}

func TestWorktreeFingerprintMarksGitStartFailuresPartial(t *testing.T) {
	t.Setenv("PATH", "")
	h := sha256.New()
	if hashGitStream(h, t.TempDir(), "status") {
		t.Fatal("Git stream started without a Git executable")
	}
	if !hashUntrackedStream(h, t.TempDir()) {
		t.Fatal("untracked stream was not partial without a Git executable")
	}
}

func BenchmarkBoundedWorktreeDigestSparse50MiBArtifact(b *testing.B) {
	repo := b.TempDir()
	cmd := exec.Command("git", "-C", repo, "init")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("git init: %v: %s", err, output)
	}
	artifact := filepath.Join(repo, "generated-artifact")
	f, err := os.Create(artifact)
	if err != nil {
		b.Fatal(err)
	}
	if err := f.Truncate(50 << 20); err != nil {
		_ = f.Close()
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if digest := boundedWorktreeDigest(repo); !strings.HasPrefix(digest, "partial:") {
			b.Fatalf("oversize sparse artifact was treated as complete: %q", digest)
		}
	}
}

type mutatingFingerprintHash struct {
	hash.Hash
	trigger string
	mutate  func()
	done    bool
}

func (h *mutatingFingerprintHash) Write(p []byte) (int, error) {
	n, err := h.Hash.Write(p)
	if !h.done && string(p) == h.trigger {
		h.done = true
		h.mutate()
	}
	return n, err
}
