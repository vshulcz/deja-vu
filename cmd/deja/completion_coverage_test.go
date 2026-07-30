package main

import (
	"strings"
	"testing"
)

// The completions are hand-maintained lists, so a new command reaches users
// without one unless something checks. `files` and `restore` shipped in 0.16.4
// and 0.16.5 and were in none of the three shells.
func TestCompletionsListEveryUserFacingCommand(t *testing.T) {
	// Hooks are invoked by agents, not typed; warmup-status is internal.
	internal := map[string]bool{
		"hook-context": true, "hook-prompt": true, "hook-precompact": true,
		"hook-antigravity": true, "hook-goose": true, "hook-refresh": true,
		"warmup-status": true, "mcp": true,
	}
	shells := map[string]string{
		"bash": bashCompletion,
		"zsh":  zshCompletion,
		"fish": fishCompletion,
	}
	for name := range commands {
		if internal[name] || strings.HasPrefix(name, "-") {
			continue
		}
		for shell, script := range shells {
			if !strings.Contains(script, name) {
				t.Errorf("%s completion does not offer %q", shell, name)
			}
		}
	}
	// And the reverse: a completion offering something that no longer exists
	// teaches a wrong command.
	for _, name := range []string{"files", "restore", "view"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("%q is completed but not dispatched", name)
		}
	}
}
