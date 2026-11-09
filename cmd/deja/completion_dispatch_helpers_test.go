package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
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

func TestBenchCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "bench", ""}, []string{"recall", "context", "prompt", "block", "ingest", "read"}, []string{}},
		{[]string{"deja", "bench", "context", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "prompt", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "block", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "ingest", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "read", "--"}, []string{"--json", "--seed"}, []string{}},
		{[]string{"deja", "bench", "recall", "--"}, []string{"--json", "--seed"}, []string{}},
	})
}

func TestBlameCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "blame", "--"}, []string{"--attribution", "--git-note", "--json"}, []string{}},
		{[]string{"deja", "last", "--"}, []string{"--json"}, []string{"--attribution", "--git-note"}},
	})
}

// files and how take the same four flags, but neither had a case, so Tab
// offered the search flags: two by accident, the rest refused, and never
// --all-projects, the one each help page points at (#5067).
func TestFilesHowCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "files", "x", "--"}, []string{"--all-projects", "--limit", "--project", "--json"}, []string{"--harness", "--re"}},
		{[]string{"deja", "how", "x", "--"}, []string{"--all-projects", "--limit", "--project", "--json"}, []string{"--harness", "--re"}},
	})
}

// --quiet belongs to index. bash and zsh offered it to search, which refuses
// it, and fish and PowerShell left it off index, where it works (#5073).
func TestIndexQuietCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "search", "foo", "--"}, []string{"--limit"}, []string{"--quiet", "-quiet"}},
		{[]string{"deja", "index", "--"}, []string{"--rebuild", "--quiet"}, []string{"--json", "--limit"}},
	})
}

// The dispatch test above cannot reach bash's first-word list, which offered
// --quiet to `deja --<Tab>` and turned it into the query "--quiet ...". So each
// script names the flag exactly once, on its index line.
func TestQuietIsOfferedOnlyByIndex(t *testing.T) {
	for _, tc := range []struct{ shell, flag, line string }{
		{"bash", "--quiet", `compgen -W "--rebuild -rebuild --quiet -quiet"`},
		{"zsh", "'--quiet[", "'--rebuild[force a full rebuild]' '-rebuild[force a full rebuild]' '--quiet["},
		{"fish", "-l quiet", "__fish_seen_subcommand_from index' -l quiet"},
		{"powershell", "'--quiet'", "'index' { @('--rebuild', '-rebuild', '--quiet', '-quiet') }"},
	} {
		script := emittedCompletion(t, tc.shell)
		if n := strings.Count(script, tc.flag); n != 1 {
			t.Errorf("%s completion offers %s in %d places, want only index", tc.shell, tc.flag, n)
		}
		if !strings.Contains(script, tc.line) {
			t.Errorf("%s completion does not offer %s to index", tc.shell, tc.flag)
		}
	}
}

func TestInstallCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "install", "--"}, []string{"--no-index", "--force", "--no-guidance"}, []string{}},
		{[]string{"deja", "uninstall", "--"}, []string{"--no-guidance"}, []string{"--force", "--no-index"}},
	})
}

func TestLimitJSONCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "fix", "--"}, []string{"--limit", "--json"}, []string{"--since", "--re", "--settle", "--unsettle"}},
		{[]string{"deja", "friction", "--"}, []string{"--limit", "--json", "--settle", "--unsettle"}, []string{"--since", "--re"}},
		{[]string{"deja", "tests", "--"}, []string{"--limit", "--json"}, []string{"--since", "--re", "--settle", "--unsettle"}},
	})
}

// log and view had no case in any script, so Tab offered the search flags,
// which both refuse, and never their own (#5070).
func TestLogViewCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "log", "--"}, []string{"--last", "--json"}, []string{"--re", "--harness"}},
		{[]string{"deja", "view", "--"}, []string{"--no-open", "--out"}, []string{"--re", "--json"}},
	})
}

// promote had no case in any script, so bash, zsh and PowerShell offered the
// search flags, every one refused, and fish offered nothing — not even the four
// states --state accepts (#5066).
func TestPromoteCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "promote", "01a0", "--"}, []string{"--state", "--note", "--tag", "--to"}, []string{"--re", "--json"}},
		{[]string{"deja", "promote", "01a0", "--state", ""}, []string{"accepted", "rejected", "superseded", "stale"}, []string{"--re", "--note"}},
	})
}

// The states are substituted from the map promote checks against, like the
// roles, so a fifth state cannot leave the scripts behind.
func TestCompletionOffersEveryNoteState(t *testing.T) {
	states := make([]string, 0, len(sources.NoteStates))
	for s := range sources.NoteStates {
		states = append(states, s)
	}
	sort.Strings(states)
	want := strings.Join(states, " ")
	for _, shell := range completionTestShells {
		if script := emittedCompletion(t, shell); !strings.Contains(script, want) {
			t.Errorf("%s completion does not offer every promote state", shell)
		}
	}
}

func TestRecallCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "recall", "--"}, []string{"--project", "--harness", "--limit"}, []string{"--re", "--role", "--json"}},
		{[]string{"deja", "search", "--"}, []string{"--re", "--role"}, nil},
	})
}

// recap had no case in any script, so Tab offered the search flags: --since
// only because search has it too, and --project, which recap refuses (#5069).
func TestRecapCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "recap", "--"}, []string{"--since", "--limit", "--json"}, []string{"--project", "--re", "--harness"}},
	})
}

func TestRestoreCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "restore", "src/a.go", "--"}, []string{"--span", "--force", "--all"}, []string{"--re", "--json"}},
		{[]string{"deja", "last", "--"}, []string{"--json"}, []string{"--span", "--force"}},
	})
}

func TestRulesCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "rules", ""}, []string{"sync", "status", "candidates"}, []string{"--re", "--no-embed"}},
		{[]string{"deja", "rules", "candidates", "--"}, []string{"--json", "--limit", "--since"}, []string{"--re"}},
		{[]string{"deja", "rules", "sync", ""}, []string{}, []string{"--json", "--limit", "export", "import", "ssh"}},
		{[]string{"deja", "rules", "status", ""}, []string{}, []string{"--json", "--limit"}},
	})
}

func TestSecretsCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "secrets", "--"}, []string{"--limit", "--json", "--scrub", "--dry-run"}, []string{"--re", "--harness", "--role"}},
		{[]string{"deja", "last", "--"}, []string{"--json"}, []string{"--scrub", "--dry-run"}},
	})
}

func TestSyncCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "sync", ""}, []string{"export", "import", "ssh", "forget"}, []string{}},
		{[]string{"deja", "sync", "export", "--"}, []string{"--full", "--include-imported", "--peer"}, []string{"--both"}},
		{[]string{"deja", "sync", "ssh", "--"}, []string{"--pull", "--full", "--both"}, []string{"--include-imported", "--peer"}},
		{[]string{"deja", "sync", "forget", "--"}, []string{}, []string{"--list", "--pull", "--full", "--both", "--peer"}},
		{[]string{"deja", "sync", "import", "--"}, []string{}, []string{"--include-imported", "--peer", "--both"}},
	})
}

// update sat in the no-argument list in every script, so Tab offered nothing
// for the one flag it takes (#5072).
func TestUpdateCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "update", "--"}, []string{"--force"}, []string{"--re", "--json"}},
	})
}

// wip takes --json alone and brief takes nothing, but both fell through to the
// search flags in every script but fish, and refused each one (#5071).
func TestWipBriefCompletionDispatch(t *testing.T) {
	checkCompletionDispatch(t, []completionDispatchCase{
		{[]string{"deja", "wip", "--"}, []string{"--json"}, []string{"--re", "--harness", "--limit"}},
		{[]string{"deja", "brief", "--"}, nil, []string{"--json", "--re", "--harness", "--limit"}},
	})
}
