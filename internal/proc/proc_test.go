package proc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const helperEnv = "DEJA_PROC_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		args := os.Args[1:]
		if len(args) > 0 && args[0] == "write-file" && len(args) == 3 {
			_ = os.WriteFile(args[1], []byte(args[2]), 0o600)
			os.Exit(0)
		}
		_, _ = os.Stdout.WriteString(strings.Join(args, " "))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

func TestCommandAndCommandContext(t *testing.T) {
	exe := testExe(t)

	c := Command(exe, "alpha", "beta")
	c.Env = append(os.Environ(), helperEnv+"=1")
	out, err := c.Output()
	if err != nil {
		t.Fatalf("Command.Output: %v", err)
	}
	if got := string(out); got != "alpha beta" {
		t.Fatalf("Command output = %q, want %q", got, "alpha beta")
	}

	cc := CommandContext(t.Context(), exe, "gamma")
	cc.Env = append(os.Environ(), helperEnv+"=1")
	out, err = cc.Output()
	if err != nil {
		t.Fatalf("CommandContext.Output: %v", err)
	}
	if got := string(out); got != "gamma" {
		t.Fatalf("CommandContext output = %q, want %q", got, "gamma")
	}
}

func TestInteractiveCommandContextLeavesHideWindowUnset(t *testing.T) {
	exe := testExe(t)
	cmd := InteractiveCommandContext(t.Context(), exe, "one")
	if cmd.SysProcAttr != nil {
		t.Fatalf("expected nil SysProcAttr for InteractiveCommandContext, got %+v", cmd.SysProcAttr)
	}
}

func TestRunAndCombinedOutput(t *testing.T) {
	t.Setenv(helperEnv, "1")
	exe := testExe(t)

	out, err := Run(t.Context(), exe, "hello", "world")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(out) != "hello world" {
		t.Fatalf("Run = %q, want %q", out, "hello world")
	}

	both, err := CombinedOutput(t.Context(), exe, "combined")
	if err != nil {
		t.Fatalf("CombinedOutput: %v", err)
	}
	if string(both) != "combined" {
		t.Fatalf("CombinedOutput = %q, want %q", both, "combined")
	}
}

func TestStartDetachedPIDWritesFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "detached.txt")
	exe := testExe(t)
	cmd := CommandContext(context.Background(), exe, "write-file", target, "done")
	cmd.Env = append(os.Environ(), helperEnv+"=1")

	pid, err := StartDetachedPID(cmd)
	if err != nil {
		t.Fatalf("StartDetachedPID: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("expected positive pid, got %d", pid)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, readErr := os.ReadFile(target)
		if readErr == nil && string(b) == "done" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("detached child pid=%d never wrote %s", pid, target)
}

func TestStartDetachedErrorOnNil(t *testing.T) {
	if _, err := StartDetachedPID(nil); err == nil {
		t.Fatal("expected error for nil *exec.Cmd")
	}
	var bad *exec.Cmd = Command("definitely-nonexistent-deja-test-binary-xyz")
	if err := StartDetached(bad); err == nil {
		t.Fatal("expected error when starting nonexistent binary")
	}
}
