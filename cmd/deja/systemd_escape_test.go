package main

import (
	"strings"
	"testing"
)

// The plist writer escapes the binary path because a home directory is a
// user-chosen string. The unit file wrote it raw, so a path holding a systemd
// specifier, a variable, a C escape or a quote ran something other than what
// deja installed — or did not parse at all (#2621).
func TestSyncTimerUnitEscapesThePath(t *testing.T) {
	cases := []struct {
		name, exe, want string
	}{
		{"percent is a specifier", "/Users/50%off/bin/deja", `ExecStart="/Users/50%%off/bin/deja" sync`},
		{"dollar is a variable", "/Users/me/$HOME/deja", `ExecStart="/Users/me/$$HOME/deja" sync`},
		{"backslash is an escape", `/Users/me/a\b/deja`, `ExecStart="/Users/me/a\\b/deja" sync`},
		{"a quote ends the string", `/Users/me/"q"/deja`, `ExecStart="/Users/me/\"q\"/deja" sync`},
		{"a space needs the quotes", "/Users/me/My Tools/deja", `ExecStart="/Users/me/My Tools/deja" sync`},
		{"an ordinary path is untouched", "/usr/local/bin/deja", `ExecStart="/usr/local/bin/deja" sync`},
		// A backslash before a quote: each is escaped once, and neither
		// escape is fed back through the other.
		{"a backslash then a quote", `/Users/me/a\"b/deja`, `ExecStart="/Users/me/a\\\"b/deja" sync`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unit := syncAutoService(tc.exe)
			if !strings.Contains(unit, tc.want) {
				t.Fatalf("want the line\n  %s\nin\n%s", tc.want, unit)
			}
		})
	}
}

// The timer file names no path, so it has nothing to escape; this only pins
// that the two files stay in step if one of them grows one.
func TestSyncTimerFilesNameTheSameUnit(t *testing.T) {
	if !strings.Contains(syncAutoTimer(), "[Timer]") || strings.Contains(syncAutoTimer(), "ExecStart") {
		t.Fatalf("the timer file changed shape:\n%s", syncAutoTimer())
	}
}
