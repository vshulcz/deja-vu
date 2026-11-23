//go:build !windows

package hideconsole

import "os/exec"

// Apply does nothing outside Windows: a child shares its parent's terminal,
// or has none, and opens no window.
func Apply(cmd *exec.Cmd) {
	_ = cmd
}
