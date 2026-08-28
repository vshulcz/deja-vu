package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

var completionTestShells = []string{"bash", "zsh", "fish", "powershell"}

func emittedCompletion(t *testing.T, shell string) string {
	t.Helper()
	out, err := captureRun(t, "completion", shell)
	if err != nil {
		t.Fatalf("completion %s: %v", shell, err)
	}
	return out
}

// Tab completion offered eleven harness names while deja read eighteen, in six
// hand-maintained copies of one list — hermes, goose, kimi, cline, roo,
// openclaw and zed were all missing. The lists are substituted from the
// registry now, and this is what keeps the next harness from repeating it.
func TestCompletionOffersEveryHarness(t *testing.T) {
	names := sources.HarnessNames()
	if len(names) < 10 {
		t.Fatalf("registry returned %d names, so this test proves nothing", len(names))
	}
	// The whole list, in registry order, not each name somewhere in the script:
	// every harness name also appears among the install targets, so looking for
	// them one at a time passes on a completion that offers none of them.
	want := strings.Join(names, " ")
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer the registry's harnesses", shell)
		}
	}
}

// The same for the agents `handoff --to` accepts: that list is a function, and
// the shells held a copy of it from when it was shorter.
func TestCompletionOffersEveryHandoffTarget(t *testing.T) {
	targets := handoffTargets()
	if len(targets) < 5 {
		t.Fatalf("handoffTargets returned %d, so this test proves nothing", len(targets))
	}
	want := strings.Join(targets, " ")
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer every handoff target", shell)
		}
	}
}

// And nothing is left holding a placeholder: a substitution that stops matching
// would otherwise ship the marker to the reader's shell.
func TestCompletionSubstitutesEveryPlaceholder(t *testing.T) {
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); strings.Contains(script, "%HARNESSES%") ||
			strings.Contains(script, "%HANDOFF_TARGETS%") || strings.Contains(script, "%INSTALL_TARGETS%") {
			t.Errorf("%s completion still carries a placeholder", shell)
		}
	}
}

// The roles list was the one hand-maintained copy left: seven of them across
// the three shells, all reading "user assistant tool" while `--role` accepted
// four more — tool-output, files, command and edit, the channels that record
// which files a session touched, which commands it ran and which edits it made
// (#1658). Substituted from knownRoles now, like the harnesses above.
func TestCompletionOffersEveryRole(t *testing.T) {
	if len(knownRoles) < 5 {
		t.Fatalf("knownRoles holds %d entries, so this test proves nothing", len(knownRoles))
	}
	want := strings.Join(knownRoles, " ")
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script := emittedCompletion(t, shell)
		if !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer every role deja accepts", shell)
		}
		// And no copy of the old short list may survive alongside it.
		if strings.Contains(script, "user assistant tool)") || strings.Contains(script, "'user assistant tool'") {
			t.Errorf("%s completion still holds a hand-written role list", shell)
		}
	}
}
