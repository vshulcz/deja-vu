package main

import (
	"os/exec"
	"strings"
	"testing"
)

// A shell with `set -u` is a shell people actually run — it is standard advice
// and it follows them into interactive use. The function guarded `prev` against
// an unbound index and read COMP_WORDS[1] and [2] bare, so the first Tab after
// `deja ` printed "COMP_WORDS[2]: unbound variable" and no candidates (#1656).
func TestBashCompletionSurvivesNounset(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	script := bashCompletion
	// The state right after `deja <Tab>`: two words, the cursor on the second.
	const drive = `
set -u
COMP_WORDS=(deja '')
COMP_CWORD=1
_deja_completion
printf '%s\n' "${#COMPREPLY[@]}"
`
	out, err := exec.Command(bash, "-c", script+drive).CombinedOutput()
	got := string(out)
	if err != nil {
		t.Fatalf("completion failed under set -u: %v\n%s", err, got)
	}
	if strings.Contains(got, "unbound variable") {
		t.Errorf("unbound variable under set -u:\n%s", got)
	}
	if strings.TrimSpace(got) == "0" {
		t.Errorf("no candidates offered:\n%s", got)
	}
}
