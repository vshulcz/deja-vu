//go:build !windows

package main

import (
	"os/signal"
	"syscall"
)

// ignoreSIGPIPE lets a write on a stdout nobody is reading fail with EPIPE
// instead of ending the process.
//
// Install reports each edit on a line of its own, and the reader may be
// `head -1`: the pipe closes after the first of those lines, and Go's answer
// to the next write on stdout is SIGPIPE — which kills the process outright.
// The config edits are already on disk by then, and `defer recordWiring`,
// the call that remembers them for uninstall, never runs. Aider made it
// visible: the record of the `read:` scalar install had turned into a list
// was lost with it, so uninstall left the list form behind (#5322).
//
// Ignored, the write returns EPIPE, the run goes on losing output rather than
// state, and the deferred record lands.
func ignoreSIGPIPE() {
	signal.Ignore(syscall.SIGPIPE)
}
