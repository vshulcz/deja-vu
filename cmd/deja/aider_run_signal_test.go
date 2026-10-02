//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A SIGTERM or a closed terminal ended deja and left the aider it started
// running with nobody waiting on it, and the digest in the file. The signal
// goes on to aider, and deja restores the file once aider is gone.
func TestDejaAiderPassesSIGTERMToAider(t *testing.T) {
	if os.Getenv("DEJA_AIDER_SIGNAL_HELPER") != "" {
		return
	}
	home := t.TempDir()
	pidFile, got := filepath.Join(home, "aider.pid"), filepath.Join(home, "got-term")
	aiderStandIn(t, home, "echo $$ > '"+pidFile+"'\ntrap \"echo term > '"+got+"'; exit 0\" TERM\nwhile :; do sleep 0.05; done\n")
	cmd := exec.Command(os.Args[0], "-test.run=^TestDejaAiderSignalHelper$")
	cmd.Env = append(os.Environ(), "DEJA_AIDER_SIGNAL_HELPER="+home)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, pidFile)
	time.Sleep(100 * time.Millisecond) // the pid is written before the trap is set
	b, _ := os.ReadFile(pidFile)
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	if _, err := os.Stat(got); err != nil {
		t.Errorf("aider never got the SIGTERM deja did; it is still running on its own")
	}
	if after, _ := os.ReadFile(aiderContextPath()); strings.TrimSpace(string(after)) != strings.TrimSpace(aiderPlaceholder) {
		t.Errorf("the file kept the digest:\n%s", after)
	}
}

// TestDejaAiderSignalHelper is the deja process the test above signals.
func TestDejaAiderSignalHelper(t *testing.T) {
	if os.Getenv("DEJA_AIDER_SIGNAL_HELPER") == "" {
		t.Skip("run by TestDejaAiderPassesSIGTERMToAider")
	}
	// TestMain hands this process a home of its own; the test's is the one
	// with the stand-in aider and the digest.
	home := os.Getenv("DEJA_AIDER_SIGNAL_HELPER")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("PATH", filepath.Join(home, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(filepath.Join(home, "proj"))
	_ = cmdAider(filepath.Join(home, "index"), []string{"x"}, "")
}
