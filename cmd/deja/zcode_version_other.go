//go:build !windows

package main

// zcodeDesktopVersion is indirected so install and doctor can be tested
// without a desktop build present.
var zcodeDesktopVersion = zcodeDesktopVersionReal

// ZCode's desktop version resource is a Windows PE resource. On other
// platforms deja falls back to probing the config files themselves, which
// needs no version at all.
func zcodeDesktopVersionReal() (v [3]int, ok bool) {
	return v, false
}
