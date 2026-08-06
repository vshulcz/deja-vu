//go:build !windows

package main

import "os"

// nullDeviceInfo is os.DevNull's identity, resolved once. A platform where it
// cannot be stat'ed reports nothing rather than guessing, which leaves the
// character-device test alone there.
var nullDeviceInfo = func() os.FileInfo {
	fi, err := os.Stat(os.DevNull)
	if err != nil {
		return nil
	}
	return fi
}()

// isNullDevice reports whether f is /dev/null. Unix only: NUL on Windows is a
// character device too, but its stat carries none of the volume and index
// information os.SameFile compares, so the same check there would answer for
// every character device rather than for one.
func isNullDevice(fi os.FileInfo) bool {
	return nullDeviceInfo != nil && os.SameFile(fi, nullDeviceInfo)
}
