package index

import "strings"

// A remedy is usually the failing command, corrected.
//
// The miner asks two things of a candidate pair: does the remedy name something
// the error named, or did another session do the same thing after the same
// error. Both are about words, and both miss the commonest shape there is —
// the agent runs a command, it fails, and the next thing it runs is that same
// command with the glob quoted, the flag dropped or the path fixed.
//
// Counted over 2,953 failed commands in real transcripts: 4% were followed by
// the identical command working (a retry of a flake, median zero steps in
// between), 64% by a changed command running the same program, and 16% by one
// that also kept most of the failing command. That last group is 479 pairs on
// one machine, against the 112 confirmed pairs the word rules mine from the
// same corpus — and it is better evidence, because the remedy is not a command
// that happened to follow, it is the command that failed, working.
const (
	// repairedOverlap is how much of the failing command has to survive. At
	// 0.6 a quoted glob, a dropped flag and a corrected path all still count,
	// while `cd one-place` after `cd another` does not — that pair shares a
	// program and nothing else, and it was the single most common shape below
	// this bound.
	repairedOverlap = 0.6
	// repairedMinTokens keeps one-word commands out. `make` after `make` is
	// the identical command, and `ls` after `pwd` is not a repair of anything.
	repairedMinTokens = 2
)

// repairedVariant reports whether cmd is failed, corrected: the same program,
// most of the same words, and not the same line over again.
func repairedVariant(failed, cmd string) bool {
	failed, cmd = strings.TrimSpace(failed), strings.TrimSpace(cmd)
	if failed == "" || cmd == "" || failed == cmd {
		return false
	}
	a, b := commandTokens(failed), commandTokens(cmd)
	if len(a) < repairedMinTokens || len(b) < repairedMinTokens {
		return false
	}
	prog := commandProgram(a)
	if prog == "" || prog != commandProgram(b) {
		return false
	}
	// Going somewhere else is not a repair of not being able to go here, and
	// this is where the noise was: `cd` alone is 705 of the 1,892 same-program
	// successes, the largest single source. The commands below carry no work,
	// so a corrected one of them is nothing to hand an agent even when it is
	// genuinely the fix.
	if navigationCommands[prog] {
		return false
	}
	return tokenOverlap(a, b) >= repairedOverlap
}

// navigationCommands are the ones that move around or print something rather
// than do anything. A remedy has to be worth running.
var navigationCommands = map[string]bool{
	"cd": true, "ls": true, "pwd": true, "cat": true, "echo": true,
	"head": true, "tail": true, "which": true, "less": true, "more": true,
}

// commandTokens splits a command into the words a comparison can be made on.
//
// The shell prompt some harnesses store with the command goes first. Left in,
// it is the program of every command ever recorded: `$` equals `$`, so the
// same-program test below passed for any two lines and the navigation guard
// guarded nothing — measured on a real store, 21 of the 163 repaired pairs
// served ran a different program (`go test` answered by `rm`, `git` by `go`)
// and three more were `cd` somewhere else.
func commandTokens(cmd string) []string {
	f := strings.Fields(cmd)
	if len(f) > 0 && f[0] == "$" {
		return f[1:]
	}
	return f
}

// commandProgram is what the command runs, without the path it was found at
// and without the environment assignments and wrappers in front of it.
func commandProgram(tokens []string) string {
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if strings.Contains(t, "=") {
			continue
		}
		switch t {
		case "sudo", "env", "time", "nohup", "exec", "command":
			continue
		case "timeout", "gtimeout", "stdbuf", "nice":
			// The wrapper takes a duration before the command it runs, and
			// `timeout 90 ssh …` runs ssh — dropping a wrapper the shell cannot
			// find is the repair for the wall this machine hits most, so both
			// sides have to name the program that does the work. With no
			// duration after it the wrapper is the program itself.
			if i+1 < len(tokens) && isDurationToken(tokens[i+1]) {
				i++
				continue
			}
		}
		if i := strings.LastIndex(t, "/"); i >= 0 {
			t = t[i+1:]
		}
		return t
	}
	return ""
}

// isDurationToken reports whether the token is a plain duration — the argument
// `timeout` and its kin take before the command they run.
func isDurationToken(t string) bool {
	t = strings.TrimRight(t, "smhd")
	if t == "" {
		return false
	}
	for _, r := range t {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// tokenOverlap is how much of the two commands is the same words, counted as
// the shared multiset over the longer side. Multiset rather than set: a command
// that repeats a flag is not more similar for repeating it.
func tokenOverlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	counts := make(map[string]int, len(a))
	for _, t := range a {
		counts[t]++
	}
	shared := 0
	for _, t := range b {
		if counts[t] > 0 {
			counts[t]--
			shared++
		}
	}
	longer := len(a)
	if len(b) > longer {
		longer = len(b)
	}
	return float64(shared) / float64(longer)
}
