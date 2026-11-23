//go:build windows

package hideconsole

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW from the Win32 process creation flags.
// It is spelled out here because deja takes no runtime Go dependency.
const createNoWindow = 0x08000000

// Apply sets cmd to start with a console that has no window. It keeps any
// SysProcAttr the caller already set, so it can be called before or after
// other process settings.
func Apply(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
