package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// One target that refuses to be written used to end the run: an uninstall
// stopped at the first locked path, left every other harness wired, and handed
// back a syscall for a file nobody chose (#902).
func TestUninstallFinishesTheTargetsItCan(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	home := hermeticEnv(t)
	_ = home
	if _, err := captureRun(t, "install", "claude-auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "codex-auto"); err != nil {
		t.Fatal(err)
	}
	// install writes into the real codex home under HOME, not DEJA_CODEX_ROOT:
	// that variable relocates reads only.
	codexHooks := filepath.Join(os.Getenv("HOME"), ".codex", "hooks.json")
	before, err := os.ReadFile(codexHooks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "deja") {
		t.Fatalf("codex was not wired: %s", before)
	}

	guidance := filepath.Join(os.Getenv("HOME"), ".claude", "skills", "deja-history")
	if err := os.Chmod(guidance, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(guidance, 0o700) })

	_, err = captureRun(t, "uninstall", "--all")
	if err == nil {
		t.Fatal("a locked guidance directory was not reported")
	}
	got := err.Error()
	if !strings.Contains(got, "finished what it could") || !strings.Contains(got, "refused") {
		t.Errorf("error does not say what happened: %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Errorf("error does not name the cause: %q", got)
	}

	// The rest of the run still happened.
	after, err := os.ReadFile(codexHooks)
	if err == nil && strings.Contains(string(after), "deja") {
		t.Errorf("codex stayed wired because another target refused: %s", after)
	}
}
