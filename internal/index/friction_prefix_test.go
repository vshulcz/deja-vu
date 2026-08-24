package index

import "testing"

// normalizeFriction already strips the shell's `zsh:1: ` position prefix so one
// missing command counts once. Two other prefixes are just as mechanical: the
// `E   ` pytest puts in front of the failing line, and the timestamp docker,
// journalctl and CI put in front of everything. Left in, one error counted as
// two walls and `deja fix` missed the paste (#1637).
func TestNormalizeFrictionStripsMechanicalPrefixes(t *testing.T) {
	const want = "ModuleNotFoundError: No module named 'app.db'"
	for _, in := range []string{
		want,
		"E   " + want,
		"E " + want,
		"2026-07-12T10:00:00Z " + want,
		"2026-07-12 10:00:00 " + want,
		"[2026-07-12T10:00:00Z] " + want,
	} {
		if got := normalizeFriction(in); got != want {
			t.Errorf("normalizeFriction(%q) = %q, want %q", in, got, want)
		}
	}
	// The controls: nothing that carries meaning may be trimmed.
	for in, keep := range map[string]string{
		"Error: cannot find module 'express'": "Error: cannot find module 'express'",
		"E2BIG: argument list too long":       "E2BIG: argument list too long",
		"Exception: no route to host":         "Exception: no route to host",
		"zsh:1: command not found: timeout":   "command not found: timeout",
		"npm ERR! code ELIFECYCLE":            "npm ERR! code ELIFECYCLE",
		"EACCES: permission denied, open 'x'": "EACCES: permission denied, open 'x'",
		"E: Unable to locate package golang":  "E: Unable to locate package golang",
		"2026 is not a year we support":       "2026 is not a year we support",
	} {
		if got := normalizeFriction(in); got != keep {
			t.Errorf("normalizeFriction(%q) = %q, want %q", in, got, keep)
		}
	}
}
