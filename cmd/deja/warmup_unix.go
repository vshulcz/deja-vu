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

// startDetachedPID is startDetached that also names the child: the warmup
// records the pid in its claim while the background Wait reaps the child when
// it exits so long-running parents (`deja mcp`) do not accumulate zombies.
func startDetachedPID(cmd *exec.Cmd) (int, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() {
		_ = cmd.Wait()
	}()
	return pid, nil
}
