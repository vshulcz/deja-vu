// Package gitread runs git for the questions deja asks of a repository: what
// changed, who wrote a line, which worktrees exist. Every one of them is a
// read, and most run from hooks, so none may touch the repository or the
// network.
package gitread

import (
	"context"
	"os"
	"os/exec"
)

// Env is what every read-only git call adds to the environment.
//
// GIT_OPTIONAL_LOCKS=0: status and diff refresh the index and take its lock
// when they can; a hook doing that in the background holds index.lock under
// the user's own git commands.
//
// GIT_NO_LAZY_FETCH=1: in a partial clone, a missing blob is fetched from the
// promisor remote on demand. Rename detection in log and diff, blame and show
// all read blobs, so a session-start digest would open a connection to origin.
// With lazy fetching off the call fails instead, and a caller that cannot
// answer says nothing. git before 2.44 ignores the variable.
var Env = []string{"GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1"}

// Command is git with args, bounded by ctx, set up for reading.
func Command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), Env...)
	return cmd
}
