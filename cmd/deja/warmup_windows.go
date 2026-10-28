//go:build windows

package main

import "os/exec"

func startDetached(cmd *exec.Cmd) error {
	_, err := startDetachedPID(cmd)
	return err
}

// startDetachedPID is startDetached that also names the child: Release
// closes the process handle and forgets the pid, and the warmup records the
// pid in its claim.
func startDetachedPID(cmd *exec.Cmd) (int, error) {
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	return pid, cmd.Process.Release()
}
