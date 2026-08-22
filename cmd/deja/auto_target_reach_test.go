package main

import (
	"strings"
	"testing"
)

// `deja install --auto` is the command the docs put in front of everyone, and
// the mapping from "this harness is on the machine" to "install its deepest
// integration" used to be a switch someone had to remember to extend. Two
// harnesses landed after it was written and fell through to the default, so
// --auto wired their MCP server and left auto-recall off — on a machine that
// had it, with nothing in the output saying so.
func TestEveryAutoTargetIsReachableFromInstallAuto(t *testing.T) {
	for _, name := range installTargetNames() {
		if !strings.HasSuffix(name, "-auto") {
			continue
		}
		base := strings.TrimSuffix(name, "-auto")
		// Detection reports Claude under its own name.
		detected := base
		if base == "claude" {
			detected = "claude-code"
		}
		if got := autoTargetFor(detected); got != name {
			t.Errorf("a machine with %s installs %q, not %q — auto-recall is silently skipped", detected, got, name)
		}
	}

	// A harness with no -auto target keeps the plain one rather than being
	// dropped: for the IDE extensions the MCP server is the whole integration.
	for _, plain := range []string{"windsurf", "roo"} {
		if got := autoTargetFor(plain); got != plain && !strings.HasPrefix(got, plain+"-auto") {
			t.Errorf("autoTargetFor(%q) = %q", plain, got)
		}
	}
}
