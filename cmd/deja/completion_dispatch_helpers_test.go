package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

type completionDispatchCase struct{ words, want, absent []string }

func checkCompletionDispatch(t *testing.T, cases []completionDispatchCase) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	for _, shell := range completionTestShells {
		t.Run(shell, func(t *testing.T) {
			binary := shell
			if shell == "powershell" {
				binary = "pwsh"
			}
			path, err := exec.LookPath(binary)
			if err != nil {
				t.Skipf("%s not on PATH", binary)
			}
			script := emittedCompletion(t, shell)
			for _, tc := range cases {
				t.Run(strings.Join(tc.words, " "), func(t *testing.T) {
					quoted := make([]string, len(tc.words))
					for i, word := range tc.words {
						quoted[i] = fmt.Sprintf("%q", word)
					}
					input := strings.Join(tc.words, " ")
					var args []string
					switch shell {
					case "bash":
						drive := fmt.Sprintf("\nCOMP_WORDS=(%s)\nCOMP_CWORD=%d\n_deja_completion\nprintf '%%s\\n' \"${COMPREPLY[@]}\"\n", strings.Join(quoted, " "), len(tc.words)-1)
						args = []string{"--noprofile", "--norc", "-c", script + drive}
					case "zsh":
						// Capture the dispatched specs without depending on an interactive ZLE.
						stub := "compdef() { :; }; _arguments() { print -rl -- \"$@\"; }; _values() { shift; print -rl -- \"$@\"; };\n"
						drive := fmt.Sprintf("\nwords=(%s)\nCURRENT=%d\n_deja\n", strings.Join(quoted, " "), len(tc.words))
						args = []string{"-f", "-c", stub + script + drive}
					case "fish":
						args = []string{"--no-config", "-c", script + "\ncomplete -C '" + input + "'\n"}
					case "powershell":
						drive := fmt.Sprintf("\n[System.Management.Automation.CommandCompletion]::CompleteInput('%s', %d, $null).CompletionMatches | ForEach-Object { $_.CompletionText }\n", input, len(input))
						args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference = 'Stop';\n" + script + drive}
					}
					cmd := exec.Command(path, args...)
					cmd.Dir = home
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("%s completion: %v\n%s", shell, err, out)
					}
					got := map[string]bool{}
					for _, line := range strings.Split(string(out), "\n") {
						line, _, _ = strings.Cut(line, "\t")
						line, _, _ = strings.Cut(line, "[")
						got[strings.TrimSuffix(strings.TrimSpace(line), "=")] = true
					}
					for _, want := range tc.want {
						if !got[want] {
							t.Errorf("missing %q in %s", want, out)
						}
					}
					for _, absent := range tc.absent {
						if got[absent] {
							t.Errorf("unexpected %q in %s", absent, out)
						}
					}
				})
			}
		})
	}
}
