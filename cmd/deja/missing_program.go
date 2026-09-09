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
			return fmt.Sprintf("%s is not on this machine — %s ran into that.",
				prog, toolSessionCount(n))
		}
	}
	return ""
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
