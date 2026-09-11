package main

import (
	"fmt"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// A program this machine does not have is the one thing deja can say for
// certain before a command runs, and it was the one thing it did not say.
//
// The session-start block has carried it for a long time — "18 separate
// sessions hit `command not found: timeout`" — and measured on this machine's
// own transcripts, of the ten sessions that were told, nine went on to run the
// command anyway and got the same refusal. `timeout` alone: named at the start
// of ten sessions, run in nine of them. A fact stated at the start does not
// survive to the moment it matters.
//
// This is that fact, moved to the moment. The pre-tool hook already fires
// before the command runs and was silent here, because what it looks up is the
// shape of the whole command — `timeout 30 go test ./...` shares no shape with
// `timeout 600 python3 build.py`, so nothing matched. A missing program is not
// about the shape. It is about the first word.
const (
	// missingProgramMinSessions is how many separate sessions have to have been
	// refused before deja says so. One is a machine that has changed since;
	// deja does not check whether the program is there now, and must not turn
	// a stale sighting into an instruction.
	missingProgramMinSessions = 2
	// missingProgramMaxTokens bounds how much of a compound is examined. Every
	// segment after the first ran in a state the ones before it made, but a
	// missing program refuses the same way wherever it stands.
	missingProgramMaxTokens = 12
)

// missingProgramLine says that a program in this command is not on this
// machine, or "" when every program in it has run here.
func missingProgramLine(dir, cmd string) string {
	pol := policy.Load()
	allow := func(project string) bool { return pol.Allows(policy.ActivationAuto, project) }
	for _, prog := range commandPrograms(cmd) {
		_, sig, ok := index.FrictionSignature("command not found: " + prog)
		if !ok {
			continue
		}
		if n := index.FrictionSessions(dir, sig, allow); n >= missingProgramMinSessions {
			line := fmt.Sprintf("%s is not on this machine — %s ran into that.",
				prog, toolSessionCount(n))
			return line + missingProgramRemedy(dir, prog, allow)
		}
	}
	return ""
}

// missingProgramRemedy adds what this machine did instead, when what it did was
// simply to leave the program out.
//
// The line above states a fact and recommends nothing, and the note on this file
// says what that is worth: of the ten sessions told at session start that
// `timeout` was missing, nine ran it anyway. The store knows more than the fact.
// Both pairs recorded for `timeout` on this machine are the same command with
// the wrapper taken out — `timeout 12 launchctl kickstart …` followed by
// `launchctl kickstart …` — which is a remedy an agent can apply without reading
// another project's command line.
//
// Only that shape. A pair whose remedy is a different program ("use python3") is
// a claim about this machine's toolchain that one recorded run does not support,
// and a pair whose remedy is a diagnostic (`docker info | grep …`, which is what
// the store holds for docker) is not a remedy at all.
func missingProgramRemedy(dir, prog string, allow func(project string) bool) string {
	for _, p := range index.FixesFor(dir, "command not found: "+prog, 4, allow) {
		if sameCommandWithout(p.Failed, p.Command, prog) {
			return " The same command ran without it."
		}
	}
	return ""
}

// sameCommandWithout reports whether worked is failed with the program — and the
// argument a wrapper takes, like `timeout 90` — removed and nothing else
// changed.
func sameCommandWithout(failed, worked, prog string) bool {
	if failed == "" || worked == "" {
		return false
	}
	var kept []string
	dropNext := false
	found := false
	for _, f := range strings.Fields(strings.TrimPrefix(strings.TrimSpace(failed), "$ ")) {
		if dropNext {
			dropNext = false
			// A wrapper's first argument is its own — `timeout 90` — and the
			// next token after the program is dropped with it only when it is
			// not part of the work.
			if isWrapperArgument(f) {
				continue
			}
		}
		if f == prog {
			found = true
			dropNext = true
			continue
		}
		kept = append(kept, f)
	}
	if !found {
		return false
	}
	return strings.Join(kept, " ") == strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(worked), "$ ")), " ")
}

// isWrapperArgument reports whether a token is the kind of argument a wrapper
// takes rather than part of the command it wraps: a duration, a signal, a
// number.
func isWrapperArgument(tok string) bool {
	if tok == "" || strings.HasPrefix(tok, "-") {
		return false
	}
	for i, r := range tok {
		if r >= '0' && r <= '9' {
			continue
		}
		// A trailing unit is part of a duration: 90s, 2m, 1h.
		if i > 0 && (r == 's' || r == 'm' || r == 'h' || r == '.') && i == len(tok)-1 {
			continue
		}
		return false
	}
	return true
}

// commandPrograms lists what a command line invokes: the first word of each
// segment, without the environment assignments and wrappers in front of it.
// A wrapper is listed too — `sudo` and `timeout` are programs that can be
// missing themselves, and `timeout` is the one this machine is missing most.
func commandPrograms(cmd string) []string {
	var out []string
	seen := map[string]bool{}
	for _, seg := range splitCommandSegments(cmd) {
		fields := strings.Fields(seg)
		if len(fields) > missingProgramMaxTokens {
			fields = fields[:missingProgramMaxTokens]
		}
		for _, f := range fields {
			if strings.Contains(f, "=") {
				continue
			}
			// A path is a program the machine either has or does not have at
			// that path, which is a different claim from "not installed", and
			// the wall is recorded under the bare name a shell reports.
			if strings.ContainsAny(f, "/$\"'`(){}<>") {
				break
			}
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
			break
		}
	}
	return out
}

// splitCommandSegments cuts a command line at the separators that start a new
// command: a pipe, a semicolon, and the two boolean joiners.
func splitCommandSegments(cmd string) []string {
	fields := strings.FieldsFunc(cmd, func(r rune) bool {
		return r == '|' || r == ';' || r == '&' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
