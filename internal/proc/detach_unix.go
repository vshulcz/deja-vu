//go:build !windows

package proc

import (
	"errors"
	"os/exec"
	"syscall"
)

// StartDetachedPID starts cmd in its own session and reaps the child
// asynchronously so long-running parents (such as `deja mcp`) do not leave
// zombies behind.
func StartDetachedPID(cmd *exec.Cmd) (int, error) {
	if cmd == nil {
		return 0, errors.New("proc: nil *exec.Cmd")
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() {
		_ = cmd.Wait()
	}()
	return pid, nil
}
