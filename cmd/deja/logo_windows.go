//go:build windows

package main

import "os"

// isNullDevice is Unix-only; see logo_unix.go for why the same identity check
// cannot answer on Windows. Behaviour here is the character-device test alone,
// unchanged.
func isNullDevice(os.FileInfo) bool { return false }
