// Package proc centralizes subprocess construction and execution across
// deja-vu so console-window suppression (Windows) and detached spawning
// (session setup / process reaping) live behind one seam.
package proc

import (
	"context"
	"os/exec"

	"github.com/vshulcz/deja-vu/internal/hideconsole"
)

// LookPath searches for an executable named file in PATH.
func LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// Command returns an exec.Cmd configured for non-interactive execution with
// console-window suppression applied on Windows.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...) //nolint:noctx // Command builds a Cmd for detached or caller-managed lifecycles without a context.
	hideconsole.Apply(cmd)
	return cmd
}

// CommandContext returns an exec.Cmd bound to ctx and configured with
// console-window suppression applied on Windows.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hideconsole.Apply(cmd)
	return cmd
}

// InteractiveCommandContext returns an exec.Cmd bound to ctx for foreground
// terminal handoffs that inherit the caller's stdio and must not hide the
// console window.
func InteractiveCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// Run executes name with args under ctx using console-window suppression and
// returns stdout.
func Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return CommandContext(ctx, name, args...).Output()
}

// CombinedOutput executes name with args under ctx using console-window
// suppression and returns combined stdout and stderr.
func CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return CommandContext(ctx, name, args...).CombinedOutput()
}

// StartDetached starts cmd detached from the caller's lifecycle.
func StartDetached(cmd *exec.Cmd) error {
	_, err := StartDetachedPID(cmd)
	return err
}
