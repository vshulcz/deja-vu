//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func startDetached(cmd *exec.Cmd) error {
	_, err := startDetachedPID(cmd)
	return err
}

// startDetachedPID is startDetached that also names the child: Release
// forgets the pid, and the warmup records it in its claim.
func startDetachedPID(cmd *exec.Cmd) (int, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	return pid, cmd.Process.Release()
}
