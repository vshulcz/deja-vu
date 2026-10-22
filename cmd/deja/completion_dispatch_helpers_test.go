package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type completionDispatchCase struct{ words, want, absent []string }

// shellScriptCommand runs body from a file rather than through -c. Git Bash on
// Windows cut a -c argument near 8 KB, so once the bash script grew past that
// the last line arrived as `co: command not found` and only the Windows leg on
// main went red (#5030).
// pwsh -File wants a .ps1 name, the others take any.
func shellScriptCommand(ctx context.Context, t *testing.T, shell string, flags []string, body string) *exec.Cmd {
	t.Helper()
	file := filepath.Join(t.TempDir(), "completion.ps1")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return exec.CommandContext(ctx, shell, append(flags, file)...)
}

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
					var flags []string
					var body string
					switch shell {
					case "bash":
						drive := fmt.Sprintf("\nCOMP_WORDS=(%s)\nCOMP_CWORD=%d\n_deja_completion\nprintf '%%s\\n' \"${COMPREPLY[@]}\"\n", strings.Join(quoted, " "), len(tc.words)-1)
						flags, body = []string{"--noprofile", "--norc"}, script+drive
					case "zsh":
						// Capture the dispatched specs without depending on an interactive ZLE.
						stub := "compdef() { :; }; _arguments() { print -rl -- \"$@\"; }; _values() { shift; print -rl -- \"$@\"; };\n"
						drive := fmt.Sprintf("\nwords=(%s)\nCURRENT=%d\n_deja\n", strings.Join(quoted, " "), len(tc.words))
						flags, body = []string{"-f"}, stub+script+drive
					case "fish":
						flags, body = []string{"--no-config"}, script+"\ncomplete -C '"+input+"'\n"
					case "powershell":
						drive := fmt.Sprintf("\n[System.Management.Automation.CommandCompletion]::CompleteInput('%s', %d, $null).CompletionMatches | ForEach-Object { $_.CompletionText }\n", input, len(input))
						flags, body = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File"}, "$ErrorActionPreference = 'Stop';\n"+script+drive
					}
					cmd := shellScriptCommand(context.Background(), t, path, flags, body)
					cmd.Dir = home
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("%s completion: %v\n%s", shell, err, out)
					}
					got := map[string]bool{}
					for line := range strings.SplitSeq(string(out), "\n") {
						line, _, _ = strings.Cut(line, "\t")
						line, _, _ = strings.Cut(line, "[")
						// zsh marks a repeatable flag with a leading `*`; the user still types --tag.
						line = strings.TrimPrefix(strings.TrimSpace(line), "*")
						got[strings.TrimSuffix(line, "=")] = true
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
