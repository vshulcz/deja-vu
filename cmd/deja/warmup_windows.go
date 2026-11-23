//go:build windows

package main

import (
	"os/exec"

	"github.com/vshulcz/deja-vu/internal/hideconsole"
)

func startDetached(cmd *exec.Cmd) error {
	_, err := startDetachedPID(cmd)
	return err
}

// startDetachedPID is startDetached that also names the child: Release
// closes the process handle and forgets the pid, and the warmup records the
// pid in its claim. The child gets a console with no window: a background
// refresh started from a console-less host flashed a window otherwise (#6156).
func startDetachedPID(cmd *exec.Cmd) (int, error) {
	hideconsole.Apply(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	return pid, cmd.Process.Release()
}
