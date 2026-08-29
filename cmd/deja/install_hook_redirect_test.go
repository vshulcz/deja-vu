package main

import (
	"testing"
)

// A redirect written without a space ends the command word as surely as a pipe
// does. It was not in the set, so a line that already runs deja's hook was read
// as somebody else's and deja installed a second one beside it — the session
// start then injects memory twice (#2493).
func TestARedirectEndsTheSubcommand(t *testing.T) {
	const cmd = "/new/path/deja hook-context"
	for _, was := range []string{
		"/usr/local/bin/deja hook-context>>/tmp/deja.log",
		"/usr/local/bin/deja hook-context>/tmp/deja.log",
		"/usr/local/bin/deja hook-context</dev/null",
		"/usr/local/bin/deja hook-context|tee /tmp/deja.log",
	} {
		got := sessionStartCommands(t, updateClaudeHook(hookRootWith(was), "SessionStart", cmd, "", false))
		if len(got) != 1 || got[0] != was {
			t.Errorf("install turned\n  %q\ninto\n  %q\nwant the reader's line alone", was, got)
		}
	}
}

// A subcommand that is the head of a longer word is still not ours.
func TestALongerSubcommandIsNotOurs(t *testing.T) {
	const cmd = "/new/path/deja hook-context"
	was := "/usr/local/bin/deja hook-context-extra"
	got := sessionStartCommands(t, updateClaudeHook(hookRootWith(was), "SessionStart", cmd, "", false))
	if len(got) != 2 {
		t.Errorf("a different subcommand was taken for ours: %q", got)
	}
}
