package main

import (
	"strings"
	"testing"
)

// `deja install --auto` is the second command the README hands someone, and on a
// machine that has never run one of these agents it printed "no known agent
// config directories found" and stopped — a bare line read as a failure with
// nothing after it. Every other empty answer here names what it looked for and
// what to do next (#830); this holds that one to the same shape.
func TestInstallAutoOnAMachineWithNoAgentSaysWhatToDo(t *testing.T) {
	for _, uninstall := range []bool{false, true} {
		out := captureStdout(t, func() { noAgentConfigHere(uninstall) })
		if !strings.Contains(out, "no agent config found on this machine") {
			t.Errorf("uninstall=%v: does not say what happened: %q", uninstall, out)
		}
		// The number is the count of targets, not a hand-written one, or it
		// drifts the first time an agent is added.
		if !strings.Contains(out, "deja sources") {
			t.Errorf("uninstall=%v: does not point at where deja looked: %q", uninstall, out)
		}
		if strings.Contains(out, "all 0 agents") {
			t.Errorf("uninstall=%v: the count came out empty: %q", uninstall, out)
		}
		next := "run this again once an agent has written its first session"
		if uninstall {
			if strings.Contains(out, next) {
				t.Errorf("uninstall offers to wire something: %q", out)
			}
			if !strings.Contains(out, "nothing to remove") {
				t.Errorf("uninstall does not say it removed nothing: %q", out)
			}
			continue
		}
		if !strings.Contains(out, next) {
			t.Errorf("install does not say what to do next: %q", out)
		}
		if !strings.Contains(out, "deja install claude") {
			t.Errorf("install does not give a command that works: %q", out)
		}
	}
}
