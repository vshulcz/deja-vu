package main

import (
	"fmt"
	"os"
)

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
func stdinIsTerminal() bool {
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
