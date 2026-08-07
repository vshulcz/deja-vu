package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A newcomer whose only harness store is behind a permission wall was told by
// the very first command — the install — that the machine held no history at
// all. Only doctor, three commands later, named the wall.
func TestInstallNamesADeniedStoreInsteadOfClaimingNoHistory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	if err := os.MkdirAll(sourcesClaudeConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(root, "project")
	writeClaudeFixture(t, filepath.Join(proj, "session.jsonl"), "session", []string{
		`{"type":"user","sessionId":"session","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"history"}}`,
	})
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	out, err := captureRunStderr(t, "install", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no agent history detected") {
		t.Errorf("install called an unreadable store an empty machine:\n%s", out)
	}
	if !strings.Contains(out, "permission denied") || !strings.Contains(out, "deja doctor") {
		t.Errorf("install did not name the permission wall or where to see it:\n%s", out)
	}
}

// The hint under the install logo said "index 0 agent stores" in the same
// state: a zero plus an instruction that would change nothing.
func TestInstallHintDoesNotOfferToIndexZeroStores(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	if err := os.MkdirAll(sourcesClaudeConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "project", "session.jsonl"), "session", []string{
		`{"type":"user","sessionId":"session","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"history"}}`,
	})
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	got := installIndexHint(t.TempDir())
	if strings.Contains(got, "index 0 agent stores") {
		t.Errorf("hint offers to index nothing: %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Errorf("hint does not name the permission wall: %q", got)
	}
}
