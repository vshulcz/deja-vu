//go:build !unix

package sources

import "io/fs"

// ModeUnreadable is always false here: a Windows mode carries no read bit, and
// a file an ACL refuses is found when it is opened.
func ModeUnreadable(string, fs.FileInfo) bool { return false }
