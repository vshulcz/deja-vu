//go:build windows

package main

import (
	"os"
	"strings"
)

// isNullDevice reports whether fi is NUL. os.SameFile cannot answer here — a
// Windows stat of NUL carries none of the volume and index information it
// compares — so the name is the identity that is available, and NUL is a
// reserved name no ordinary file can take (#1097 fixed the redirect on unix
// and left this side taking the sink for a terminal).
func isNullDevice(fi os.FileInfo) bool {
	name := strings.ToUpper(fi.Name())
	name = strings.TrimPrefix(name, `\\.\`)
	return name == "NUL"
}
