package main

import (
	"fmt"
	"os"
)

// What a hook may spend, measured on a 1,367-session store with the index
// settled, five runs each (#2668):
//
//	per action, and the ones that must stay cheap
//	  hook-tool          10 ms
//	  hook-tool-after    10 ms
//	  hook-plan          under 10 ms
//	  hook-prompt        60 to 110 ms
//	  statusline         10 ms
//	  hook-goose         20 to 40 ms
//	  hook-goose-prompt  20 to 30 ms
//	  hook-precompact    10 to 40 ms
//
//	once per session, allowed one expensive run
//	  hook-context       0.85 s, then 50 ms
//	  hook-antigravity   0.56 s, then 10 ms
//
//	detached, off every blocking path
//	  hook-refresh       0.42 to 0.74 s, by design (see runHookRefresh)
//
// The shape matters more than the numbers. A change that adds a read to a
// per-action hook has to keep it behind the checks that decide there is nothing
// to say — which is why #2652 pays for its manifest read only after finding
// something worth saying. The session-start hooks are allowed their one
// expensive run because it happens once and its answer is the whole reason the
// session has memory.
//
// Measure with the index settled or the number is the warmup's: with a rebuild
// running, hook-context measured 0.54 to 0.83 s on every run and hook-prompt
// answered with nothing at all, both by design.
//
// This list sits beside hookAdvice because that map is the only other place
// that names hooks one by one; a hook dispatched without a line here has no
// budget to be held to, and a test says so.
//
// hookAdvice is what to reach for instead, for the hooks a person is most
// likely to type after reading them in `deja help`.
var hookAdvice = map[string]string{
	"hook-prompt":     `deja "<what you want to remember>"`,
	"hook-tool":       `deja how "<the command>" · deja blame <file>`,
	"hook-tool-after": `deja fix "<the error>"`,
	"hook-plan":       "deja check - (the same lookup, reading a plan from stdin)",
}

// hookTypedByHandNote is the line a hook prints when it was typed at a terminal
// rather than run by a harness. Each hook reads a JSON payload on stdin and
// answers with silence when there is nothing to say — the right contract for a
// hook, and unreadable for a person, who waits out the stdin bound and gets an
// empty screen. `deja check` earned the same treatment in #2564; these are
// listed in `deja help`, so they are typed (#2571).
func hookTypedByHandNote(name string, terminal bool) string {
	if !terminal {
		return ""
	}
	note := fmt.Sprintf("deja: %s is a harness hook — it reads a JSON payload on stdin and stays silent when it has nothing to add", name)
	if advice, ok := hookAdvice[name]; ok {
		note += "\ndeja: for the same memory by hand: " + advice
	}
	return note
}

// stdinIsTerminal reports that nothing is piped in. Same test `fix` uses before
// reading stdin, for the same reason: reading a terminal blocks with no prompt
// and reads as a hang.
//
// A variable so a test can be the reader as well as the harness: a test binary
// never has a terminal on stdin, so the branch written for a person typing
// could not otherwise be exercised (#2718).
var stdinIsTerminal = func() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// sayIfTypedByHand prints the note and reports whether the command should stop.
func sayIfTypedByHand(name string) bool {
	note := hookTypedByHandNote(name, stdinIsTerminal())
	if note == "" {
		return false
	}
	fmt.Fprintln(os.Stderr, note)
	return true
}
