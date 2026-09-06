package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCompletionSearchLimit(t *testing.T) {
	withTempStores(t)
	// Check the search declarations, not merely the presence of --limit:
	// show already has its own, separate limit option.
	for _, tc := range []struct {
		shell, start, end, want string
	}{
		{"bash", "        *)", "    esac", "--limit"},
		{"zsh", "    *)", "  esac", "--limit=[max sessions to return (1-100)]:count:"},
		{"fish", "complete -c deja -n '__deja_needs_command' -l limit", "\n", "-r"},
		{"fish", "complete -c deja -n '__fish_seen_subcommand_from search' -l limit", "\n", "-r"},
		{"powershell", "$defaultOptions = @(", ")", "'--limit'"},
	} {
		t.Run(tc.shell+"/"+tc.start, func(t *testing.T) {
			_, section, ok := strings.Cut(emittedCompletion(t, tc.shell), tc.start)
			if !ok {
				t.Fatalf("missing search declaration %q", tc.start)
			}
			section, _, _ = strings.Cut(section, tc.end)
			if !strings.Contains(section, tc.want) {
				t.Errorf("search declaration %q missing %q", section, tc.want)
			}
		})
	}
}

func TestBashCompletionLimitScope(t *testing.T) {
	withTempStores(t)
	t.Setenv("BASH_ENV", "")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	for _, tc := range []struct {
		words string
		want  bool
	}{
		{"deja --li", true},
		{"deja search --li", true},
		{"deja search query --li", true},
		{"deja query --li", true},
		{"deja show session --li", true},
		{"deja last --li", false},
		{"deja doctor --li", false},
		{"deja search --harness --li", false},
	} {
		t.Run(tc.words, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// words contains only the fixed test cases above, never user input.
			drive := "\nCOMP_WORDS=(" + tc.words + ")\nCOMP_CWORD=$((${#COMP_WORDS[@]}-1))\n_deja_completion\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n"
			out, err := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", emittedCompletion(t, "bash")+drive).CombinedOutput()
			if err != nil {
				t.Fatalf("bash completion failed: %v\n%s", err, out)
			}
			got := false
			for _, candidate := range strings.Fields(string(out)) {
				got = got || candidate == "--limit"
			}
			if got != tc.want {
				t.Errorf("offers --limit = %v, want %v; candidates: %s", got, tc.want, out)
			}
		})
	}
}
