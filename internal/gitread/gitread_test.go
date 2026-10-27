package gitread

import (
	"os"
	"slices"
	"testing"
)

// Command is only set up here, never run, so the test holds on a machine
// without git. The variables go after the parent environment because exec
// keeps the last value of a repeated key: a user who exports
// GIT_OPTIONAL_LOCKS=1 must still get a hook that takes no lock.
func TestCommandIsGitWithTheReadOnlyEnvironmentLast(t *testing.T) {
	t.Setenv("GIT_OPTIONAL_LOCKS", "1")
	t.Setenv("GIT_NO_LAZY_FETCH", "0")
	parent := os.Environ()

	cmd := Command(t.Context(), "status")

	if want := []string{"git", "status"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("args are %q, want %q", cmd.Args, want)
	}
	if len(cmd.Env) != len(parent)+len(Env) {
		t.Fatalf("env has %d entries, want the parent's %d and %q", len(cmd.Env), len(parent), Env)
	}
	if !slices.Equal(cmd.Env[:len(parent)], parent) {
		t.Error("env does not start with the parent environment")
	}
	// The literal values, not Env: a test against Env passes whatever Env holds.
	want := []string{"GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1"}
	if got := cmd.Env[len(parent):]; !slices.Equal(got, want) {
		t.Errorf("env ends with %q, want %q", got, want)
	}
	// CommandContext is what sets Cancel; plain Command leaves it nil, and a
	// hook's git would then outlive the hook's deadline.
	if cmd.Cancel == nil {
		t.Error("command is not bounded by ctx")
	}
}
