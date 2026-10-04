package index

import (
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A command the shell could not run is a different kind of failure from one
// that ran and failed. `(eval):1: == not found`, `gh: command not found` and
// `No module named 'requests'` say nothing about the code the session was
// working on; they say the line itself could not start. What answers one is
// that line again in a form that runs — the word quoted, the wrapper dropped,
// another program doing the same job — and never the next file the session
// happened to change.
//
// Measured over the repeat failures of 60 days on one machine: of 166 Claude
// moments deja held an answer for 100, and 9 helped, all of them `timeout`
// dropped from a command that then worked. The `== not found` wall alone was 57
// moments, and deja answered it with "changed next:" and a file that had
// nothing to do with it, mined from whatever the session edited after it.

// envPhrases are how an interpreter says the program or module named in the
// line is not there. Already friction phrases; here they mark the class.
var envPhrases = []string{"command not found", "no module named", "modulenotfounderror"}

// commandLevelFailure reports whether the error in text is the shell or an
// interpreter refusing to start the command, rather than the command running
// and failing. line is the normalised friction line found in text.
func commandLevelFailure(text, line string) bool {
	low := strings.ToLower(line)
	for _, p := range envPhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	// The shell's own position marker is stripped from the stored line, so
	// the raw line it came from is what says the shell printed it.
	for _, raw := range strings.Split(text, "\n") {
		if !shellErrorPrefix(raw) {
			continue
		}
		if l, ok := FrictionLine(raw); ok && l == line {
			return true
		}
	}
	return false
}

var missingProgramRE = []*regexp.Regexp{
	regexp.MustCompile(`command not found: ([\w.+-]+)`),
	regexp.MustCompile(`([\w.+-]+): command not found`),
}

// missingProgram is the program an error says the shell could not find, or "".
func missingProgram(line string) string {
	for _, re := range missingProgramRE {
		if m := re.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// commandRunningBefore is the line that ran prog in the nearest command above
// i, within the window a remedy is looked for in. The line, not the command's
// first: `/bin/bash: line 9: gh: command not found` came out of a script whose
// ninth line called gh. With no program to go by it is the nearest command's
// first line: `No module named 'requests'` names no program, and the batch it
// came out of ran that command a few records up.
func commandRunningBefore(ms []model.Message, i int, prog string) string {
	for k := i - 1; k >= 0 && k >= i-fixLookAhead; k-- {
		if ms[k].Role != roleCommand {
			continue
		}
		if prog == "" {
			return strings.TrimSpace(firstLineOf(ms[k].Text))
		}
		for _, line := range strings.Split(ms[k].Text, "\n") {
			line = strings.TrimSpace(line)
			for _, part := range commandParts(line) {
				if commandProgram(commandTokens(part)) == prog {
					return line
				}
			}
		}
	}
	return ""
}

// envRemedy reports whether cmd answers a command-level failure of failed:
// the same command in a form that runs, or the missing program's job done by
// another one.
func envRemedy(line, failed, cmd string) (repaired, substituted bool) {
	failed, cmd = strings.TrimSpace(withoutExitStatus(failed)), strings.TrimSpace(withoutExitStatus(cmd))
	if repairedVariant(failed, cmd) {
		return true, false
	}
	// `cd wt && timeout 30 go test ./x` is repaired by `cd wt && go test ./x`,
	// and the shared `cd` made the program of both sides a navigation the rule
	// above turns away. What changed is after it. The wrapper the shell could
	// not find is not part of the comparison either: `timeout 90 make check`
	// and `make check` share two words of four, and they are the same command.
	a, b := withoutSharedLead(commandParts(failed), commandParts(cmd))
	if len(a) > 0 && len(b) > 0 {
		x, y := withoutWrappers(a), withoutWrappers(b)
		if x == y && failed != cmd || repairedVariant(x, y) {
			return true, false
		}
	}
	return false, substitutedProgram(line, failed, cmd)
}

// withoutWrappers joins the parts back with each one's wrapper dropped: the
// `timeout 90` and `nohup` in front of the program that does the work.
func withoutWrappers(parts []string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := commandTokens(p)
		for len(t) > 0 {
			switch t[0] {
			case "timeout", "gtimeout", "stdbuf", "nice":
				if len(t) > 1 && isDurationToken(t[1]) {
					t = t[2:]
					continue
				}
			case "time", "nohup", "exec", "command":
				t = t[1:]
				continue
			}
			break
		}
		out = append(out, strings.Join(t, " "))
	}
	return strings.Join(out, " && ")
}

// withoutSharedLead drops the leading parts two commands have in common.
func withoutSharedLead(a, b []string) ([]string, []string) {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return a[i:], b[i:]
}

// substituteMinShared is how many words the replacement has to keep from the
// line it replaces. One is a coincidence: `gh auth status` and `git status`
// share "status" and nothing else. Two is the same repository and issue, the
// same package and path — `gh api repos/o/r/issues/131` answered by `git clone
// https://github.com/o/r`.
const substituteMinShared = 2

// substitutedProgram reports whether cmd does the missing program's job with
// another program: the part of the failing line that ran the missing program,
// and a part of cmd that runs something else on mostly the same arguments.
func substitutedProgram(line, failed, cmd string) bool {
	missing := missingProgram(line)
	if missing == "" {
		return false
	}
	// Every part that ran it: `gh auth status && gh api repos/o/r/issues/1`
	// failed on the first and meant the second.
	want := map[string]bool{}
	for _, part := range commandParts(failed) {
		if commandProgram(commandTokens(part)) == missing {
			for t := range argumentTerms(part, missing) {
				want[t] = true
			}
		}
	}
	if len(want) < substituteMinShared {
		return false
	}
	for _, part := range commandParts(cmd) {
		prog := commandProgram(commandTokens(part))
		if prog == "" || prog == missing || navigationCommands[prog] {
			continue
		}
		shared := 0
		for t := range argumentTerms(part, prog) {
			if want[t] {
				shared++
			}
		}
		if shared >= substituteMinShared {
			return true
		}
	}
	return false
}

var argumentTermRE = regexp.MustCompile(`[A-Za-z0-9_]{3,}`)

// argumentTerms are the words a command works on, without the program that
// runs it and without the ones every command line carries.
func argumentTerms(part, prog string) map[string]bool {
	out := map[string]bool{}
	for _, t := range argumentTermRE.FindAllString(strings.ToLower(part), -1) {
		if t == prog || fixCommonTerms[t] || urlNoise[t] {
			continue
		}
		out[t] = true
	}
	return out
}

// urlNoise is what any URL or API call spells, and proves nothing about two
// commands addressing the same thing.
var urlNoise = map[string]bool{
	"www": true, "com": true, "org": true, "api": true, "json": true,
	"application": true, "accept": true, "github": true, "vnd": true,
}
