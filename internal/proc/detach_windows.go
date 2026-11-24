//go:build windows

package proc

import (
	"errors"
	"os/exec"

	"github.com/vshulcz/deja-vu/internal/hideconsole"
)

// StartDetachedPID starts cmd with console suppression and releases the
// process handle while returning its PID.
func StartDetachedPID(cmd *exec.Cmd) (int, error) {
	if cmd == nil {
		return 0, errors.New("proc: nil *exec.Cmd")
	}
	hideconsole.Apply(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	return pid, cmd.Process.Release()
}
