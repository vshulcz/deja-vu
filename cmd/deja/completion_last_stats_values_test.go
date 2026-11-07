package main

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestLastAndStatsCompletionValueDispatch(t *testing.T) {
	withTempStores(t)
	t.Setenv("BASH_ENV", "")
	cases := []struct {
		line string
		want []string
	}{
		// An empty value exposes the bug: every flag matches the empty prefix.
		{"deja last --project <Tab>", nil},
		{"deja last --since <Tab>", nil},
		{"deja last --from <Tab>", nil},
		{"deja stats --project <Tab>", nil},
		{"deja stats --since <Tab>", nil},

		// Once the value is complete, the next word may be another flag.
		{"deja last --project sample-project <Tab>", lastFlags},
		{"deja last --since 7d <Tab>", lastFlags},
		{"deja last --from local <Tab>", lastFlags},
		{"deja stats --project sample-project <Tab>", statsFlags},
		{"deja stats --since 7d <Tab>", statsFlags},

		// Known value lists and ordinary flag suggestions must still work.
		{"deja last --harness <Tab>", sources.HarnessNames()},
		{"deja last --role <Tab>", knownRoles},
		{"deja stats --harness <Tab>", sources.HarnessNames()},
		{"deja stats --role <Tab>", knownRoles},
		{"deja last <Tab>", lastFlags},
		{"deja stats <Tab>", statsFlags},
	}
	for _, shell := range []string{"bash", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.line, func(t *testing.T) {
					got := lastStatsCompletions(t, shell, tc.line)
					want := slices.Clone(tc.want)
					slices.Sort(got)
					slices.Sort(want)
					if !slices.Equal(got, want) {
						t.Errorf("offers %q, want %q", got, want)
					}
				})
			}
		})
	}
}

// Run the generated completion script as if Tab were pressed at the marked
// cursor. An empty working directory keeps filesystem fallback out of results.
func lastStatsCompletions(t *testing.T, shell, line string) []string {
	t.Helper()
	binary := shell
	if shell == "powershell" {
		binary = "pwsh"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		t.Skipf("%s not on PATH", binary)
	}
	input := strings.TrimSuffix(line, "<Tab>")
	var flags []string
	var drive string
	if shell == "bash" {
		// Bash reads the words and cursor index, then writes matches to COMPREPLY.
		// Split preserves the final empty word after an option's trailing space.
		words := strings.Split(input, " ")
		for i, word := range words {
			words[i] = fmt.Sprintf("%q", word)
		}
		drive = fmt.Sprintf(`
COMP_WORDS=(%s)
COMP_CWORD=%d
_deja_completion
printf '%%s\n' "${COMPREPLY[@]}"
`, strings.Join(words, " "), len(words)-1)
		flags = []string{"--noprofile", "--norc"}
	} else {
		// PowerShell parses the command line and asks the registered completer.
		drive = fmt.Sprintf(`
[System.Management.Automation.CommandCompletion]::CompleteInput('%s', %d, $null).CompletionMatches |
    ForEach-Object { $_.CompletionText }
`, input, len(input))
		flags = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := shellScriptCommand(ctx, t, path, flags, emittedCompletion(t, shell)+drive)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("completion: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}
