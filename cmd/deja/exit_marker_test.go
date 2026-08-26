package main

import "testing"

// The exit status is a suffix a source appends — two spaces, the marker, digits,
// end of string. Reading it back with a search for the marker anywhere cut a
// command that merely mentioned it, and called the command a failure because the
// code it parsed was `0"` (#2048).
func TestTheExitMarkerIsReadAsASuffix(t *testing.T) {
	for _, c := range []struct {
		name, cmd, want string
		ok              bool
	}{
		{"a recorded success", "go test ./...  → exit 0", "go test ./...", true},
		{"a recorded failure", "go test ./...  → exit 1", "go test ./...", false},
		{"nothing recorded", "go test ./...", "go test ./...", true},
		{"the command says it", `echo "→ exit 0"`, `echo "→ exit 0"`, true},
		{"and says it while failing", `echo "→ exit 0"  → exit 1`, `echo "→ exit 0"`, false},
		{"a grep for the marker", `grep -n "→ exit " log.txt  → exit 0`, `grep -n "→ exit " log.txt`, true},
		{"a code that is not a number", "go test ./...  → exit later", "go test ./...  → exit later", true},
		{"a many-digit code", "go test ./...  → exit 130", "go test ./...", false},
	} {
		got, ok := withoutFailedExit(c.cmd)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: withoutFailedExit(%q) = %q, %v; want %q, %v", c.name, c.cmd, got, ok, c.want, c.ok)
		}
	}
}
