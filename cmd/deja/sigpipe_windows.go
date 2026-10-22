//go:build windows

package main

// ignoreSIGPIPE does nothing on Windows (#5322): there is no SIGPIPE there, a
// write to a closed pipe fails as an ordinary error, and the deferred
// recordWiring runs all the same.
func ignoreSIGPIPE() {}
