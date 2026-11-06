package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Runs the emitted bash completion: after `show --offset` or `show --limit`
// the next word is a count, so no flag may be offered there (#5940).
func TestBashCompletionShowCounts(t *testing.T) {
	withTempStores(t)
	t.Setenv("BASH_ENV", "")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	for _, tc := range []struct {
		words     string
		wantFlags bool
	}{
		{"deja show --offset ''", false},
		{"deja show --limit ''", false},
		{"deja show abc --offset ''", false},
		{"deja show --offset 5 ''", true},
		{"deja show ''", true},
	} {
		t.Run(tc.words, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// words contains only the fixed test cases above, never user input.
			drive := "\nCOMP_WORDS=(" + tc.words + ")\nCOMP_CWORD=$((${#COMP_WORDS[@]}-1))\n_deja_completion\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n"
			out, err := shellScriptCommand(ctx, t, bash, []string{"--noprofile", "--norc"}, emittedCompletion(t, "bash")+drive).CombinedOutput()
			if err != nil {
				t.Fatalf("bash completion failed: %v\n%s", err, out)
			}
			got := strings.Fields(string(out))
			if tc.wantFlags && len(got) != len(showFlags) {
				t.Errorf("offers %q, want show's flags %q", got, showFlags)
			}
			if !tc.wantFlags && len(got) != 0 {
				t.Errorf("offers %q where a count is expected", got)
			}
		})
	}
}
