//go:build windows

package main

import "testing"

// Windows has no flock, and the lock deja takes there is a different mechanism.
// Skipping says so out loud rather than leaving a test that quietly asserts
// nothing — what the tool does while another process holds the lock is still
// worth pinning on Windows, and is not pinned here.
func holdIndexLock(t *testing.T, _ string) func() {
	t.Helper()
	t.Skip("holding the index lock from a second process needs a Windows-specific lock")
	return func() {}
}
