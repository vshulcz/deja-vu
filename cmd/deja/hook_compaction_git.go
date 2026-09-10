package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	// Keep identity resolution predictable in repositories that generate large
	// local artifacts. An incomplete fingerprint is explicitly marked partial,
	// so callers must validate it instead of treating it as a fresh cache hit.
	maxUntrackedFingerprintFiles     = 4096
	maxUntrackedFingerprintFileBytes = 1 << 20
	maxUntrackedFingerprintBytes     = 8 << 20
)

// boundedWorktreeDigest fingerprints tracked changes and a bounded amount of
// untracked content. It streams Git output and file bytes instead of retaining
// either in memory. A partial: prefix means the scan could not verify every
// untracked entry (because of a limit, an I/O error, a special file, or a
// concurrent change); callers must not use such a value as a fresh hit.
//
// The caller first verifies that root is a Git worktree. The shared deadline
// bounds all Git subprocesses together, including a very large tracked diff.
func boundedWorktreeDigest(ctx context.Context, root string) string {
	h := sha256.New()

	partial := false
	for _, command := range [][]string{
		{"status", "--porcelain=v1", "-z", "--untracked-files=no"},
		{"diff", "--no-ext-diff", "--binary"},
		{"diff", "--cached", "--no-ext-diff", "--binary"},
	} {
		hashWorktreeField(h, strings.Join(command, "\x00"))
		if !hashGitStream(ctx, h, root, command...) {
			partial = true
		}
	}
	if hashUntrackedStream(ctx, h, root) {
		partial = true
	}

	digest := hex.EncodeToString(h.Sum(nil))
	if partial {
		return "partial:" + digest
	}
	return digest
}

func compactionFreshness(root string) model.RepositoryFreshness {
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	f := model.RepositoryFreshness{CheckedAt: time.Now().UTC()}
	gitValue := func(args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	inside, err := gitValue("rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		f.Error = "Repository freshness unavailable; validate the workspace before reusing conclusions."
		return f
	}
	f.Head, err = gitValue("rev-parse", "--verify", "HEAD")
	if err != nil {
		f.Error = "Repository HEAD unavailable; validate the workspace before reusing conclusions."
	}
	f.Branch, _ = gitValue("symbolic-ref", "--short", "-q", "HEAD")
	f.WorktreeState = boundedWorktreeDigest(ctx, root)
	if strings.HasPrefix(f.WorktreeState, "partial:") {
		f.Error = "Repository fingerprint is partial; validate files and tests before reusing conclusions."
	}
	return f
}

func hashGitStream(ctx context.Context, h io.Writer, root string, args ...string) bool {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	_, copyErr := io.Copy(h, stdout)
	waitErr := cmd.Wait()
	return copyErr == nil && waitErr == nil
}

// hashUntrackedStream returns true when the resulting fingerprint is partial.
func hashUntrackedStream(ctx context.Context, h hash.Hash, root string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return true
	}
	if err := cmd.Start(); err != nil {
		return true
	}

	partial := false
	used := int64(0)
	entries := 0
	reader := bufio.NewReader(stdout)
	for {
		name, readErr := reader.ReadString('\x00')
		if len(name) > 0 {
			if !strings.HasSuffix(name, "\x00") {
				partial = true
				break
			}
			name = strings.TrimSuffix(name, "\x00")
			entries++
			if entries > maxUntrackedFingerprintFiles {
				partial = true
				break
			}
			if !hashUntrackedFile(h, root, name, &used) {
				partial = true
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			partial = true
			break
		}
	}

	if partial {
		// Stop the producer once the bounded scan cannot produce a complete
		// result. This avoids walking or buffering an arbitrarily large list.
		_ = cmd.Process.Kill()
	}
	if err := cmd.Wait(); err != nil && !partial {
		partial = true
	}
	return partial
}

func hashUntrackedFile(h hash.Hash, root, name string, used *int64) bool {
	if !safeGitRelativePath(name) {
		return false
	}
	hashWorktreeField(h, "untracked")
	hashWorktreeField(h, name)
	path := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	hashWorktreeField(h, info.Mode().String())
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return false
		}
		hashWorktreeField(h, target)
		return true
	}
	if !info.Mode().IsRegular() {
		return false
	}
	hashWorktreeField(h, "content-size")
	hashWorktreeField(h, strconv.FormatInt(info.Size(), 10))
	if info.Size() < 0 || info.Size() > maxUntrackedFingerprintFileBytes || info.Size() > maxUntrackedFingerprintBytes-*used {
		// Include stable metadata for diagnostics and to make partial values
		// useful in logs. It is not a substitute for verified content.
		hashWorktreeField(h, "unverified-size")
		hashWorktreeField(h, strconv.FormatInt(info.Size(), 10))
		hashWorktreeField(h, strconv.FormatInt(info.ModTime().UnixNano(), 10))
		return false
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	opened, err := f.Stat()
	if err != nil || !sameFingerprintFile(info, opened) {
		_ = f.Close()
		return false
	}
	n, copyErr := io.CopyN(h, f, info.Size())
	*used += n
	if copyErr != nil || n != info.Size() {
		_ = f.Close()
		return false
	}
	var extra [1]byte
	extraN, readErr := f.Read(extra[:])
	*used += int64(extraN)
	after, statErr := f.Stat()
	closeErr := f.Close()
	if extraN != 0 || (readErr != nil && readErr != io.EOF) || statErr != nil || !sameFingerprintFile(info, after) || closeErr != nil || *used > maxUntrackedFingerprintBytes {
		return false
	}
	return true
}

func sameFingerprintFile(first, second os.FileInfo) bool {
	return os.SameFile(first, second) && first.Size() == second.Size() && first.ModTime().Equal(second.ModTime())
}

func safeGitRelativePath(name string) bool {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func hashWorktreeField(h io.Writer, value string) {
	_, _ = io.WriteString(h, value)
	_, _ = h.Write([]byte{0})
}
