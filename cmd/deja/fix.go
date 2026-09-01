package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja fix` answers the question people actually type at an agent: this
// broke, what did I do last time. Measured over 10462 user turns on a
// 1165-session store, 22.4% of them are that question in some wording, and the
// answer is nearly always a command that already ran once.
//
// It matches on the error rather than on words: the text can be a whole pasted
// stack trace, and every line of it is checked against the mined pairs.

func runFix(dir string, args []string, stdout io.Writer) error {
	limit := 3
	var parts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 >= len(args) {
				return fmt.Errorf("fix: --limit wants a number")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("fix: --limit wants a positive number, got %q", args[i])
			}
			limit = n
		case "--":
			// An error line can start with a dash, and Go's own test output is
			// the commonest one a reader pastes: `deja fix -- '--- FAIL: …'`
			// (#2799).
			parts = append(parts, args[i+1:]...)
			i = len(args)
		default:
			// A flag deja does not know must not be swallowed into the error
			// text — `deja fix "..." --json` used to search for a string that
			// contained "--json" and answer "no session ran a command".
			//
			// A pasted failure line is the exception: it starts with the three
			// dashes go test writes, no flag has that shape, and refusing it
			// left the reader with a command deja itself had printed and
			// nothing that would run it (#2799).
			if strings.HasPrefix(args[i], "-") && args[i] != "-" && !looksLikeAPastedLine(args[i]) {
				return fmt.Errorf("fix: unknown flag %q", args[i])
			}
			parts = append(parts, args[i])
		}
	}
	text := strings.TrimSpace(strings.Join(parts, " "))
	if text == "" {
		// A pasted trace is many lines, and shells mangle those as arguments,
		// so it can be piped in — but only read stdin when it is actually a
		// pipe. Reading a terminal blocks the command until Ctrl-D with no
		// prompt, which reads as a hang.
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			if b, rerr := io.ReadAll(os.Stdin); rerr == nil {
				text = strings.TrimSpace(string(b))
			}
		}
	}
	if text == "" {
		return fmt.Errorf("fix: give the error text as an argument, or pipe the failing output in")
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	// The trust policy gates browsing everywhere else (search, friction,
	// files); a peer's command must not surface here when imported content is
	// withheld. FixesFor returns candidates; the cmd layer filters, as it does
	// for every other surface.
	pol := policy.Load()
	pairs := index.FixesFor(dir, text, limit, func(project string) bool {
		return pol.Allows(policy.ActivationSearch, project)
	})
	if len(pairs) == 0 {
		// One honest line. The old code tried to tell "not an error" apart from
		// "never seen it", but the test it used rejects `Error: …`, `npm ERR!`
		// and any line with a quote — the exact output people paste — so it
		// accused users of not pasting an error when they had.
		// Held-but-unconfirmed is not the same as never seen, and saying the
		// second about the first tells someone who fixed this an hour ago that
		// deja lost it (#2282).
		if index.FixCandidateSeen(dir, text, func(project string) bool {
			return pol.Allows(policy.ActivationSearch, project)
		}) {
			fmt.Fprintln(stdout, "deja: one session ran something after that error, and nothing has confirmed it worked — deja waits for a second sighting before naming a remedy")
			return nil
		}
		// The lookup hashes whole error lines, so the head of one — what a
		// person types — is a different signature and matches nothing. Saying
		// the machine never saw it is then a claim about the store rather than
		// about the wording, and the store may hold exactly what was asked for
		// (#2365).
		if near := nearestRecordedError(dir, text, func(project string) bool {
			return pol.Allows(policy.ActivationSearch, project)
		}); near != "" {
			// The echo is read and the argument is pasted, so they are treated
			// differently: `%q` is Go's quoting, and a shell still expands
			// `$(…)` and backticks inside double quotes — which an error line
			// out of a transcript can carry (#2768).
			fmt.Fprintf(stdout, "deja: nothing recorded for that line — deja matches a whole error line, and the closest it holds is:\n  %s\ntry: %s\n",
				search.SafeLine(near), fixHintCommand(near))
			return nil
		}
		fmt.Fprintln(stdout, "deja: no session on this machine ran a command after that error")
		return nil
	}
	for _, p := range pairs {
		when := ""
		if !p.When.IsZero() {
			when = " · " + p.When.Local().Format("2006-01-02")
		}
		fmt.Fprintf(stdout, "%s%s\n", search.SafeLine(p.Error), when)
		if p.Edit != "" {
			// An edit is not something to run, so it is not offered as one:
			// what fixes a failing test is a change to a file, and the useful
			// half of that is which file (#2163).
			changed := "changed next"
			if p.Candidate {
				changed = "changed next, unconfirmed"
			}
			fmt.Fprintf(stdout, "  %s: %s\n", changed, search.SafeLine(p.Edit))
			continue
		}
		ran := "ran next"
		if p.Candidate {
			// One session doing something after an error is half the evidence,
			// and the reader has to be told which half they are holding.
			ran = "ran next, unconfirmed"
		}
		fmt.Fprintf(stdout, "  %s: %s\n", ran, search.SafeCommand(p.Command))
	}
	return nil
}

// nearestRecordedError names a recorded error line that contains what the
// reader typed, newest first. Containment rather than similarity: the miss this
// answers is a whole line typed short, and a reader who typed something else
// entirely deserves the plain "nothing recorded" rather than a guess.
func nearestRecordedError(dir, text string, allow func(string) bool) string {
	want := strings.ToLower(strings.TrimSpace(text))
	if len(want) < 8 {
		return ""
	}
	best := ""
	var bestWhen time.Time
	for _, p := range index.ReadFixes(dir) {
		if p.Candidate || p.Error == "" {
			continue
		}
		if allow != nil && !allow(p.Project) {
			continue
		}
		if !strings.Contains(strings.ToLower(p.Error), want) {
			continue
		}
		if best == "" || p.When.After(bestWhen) {
			best, bestWhen = p.Error, p.When
		}
	}
	return search.SafeLine(best)
}

// looksLikeAPastedLine tells a mistyped flag from an error line that happens to
// start with one. A flag is one word; what a reader pastes is a sentence, and
// the shape that brought this up is go test's "--- FAIL: TestX (0.01s)".
func looksLikeAPastedLine(arg string) bool {
	return strings.HasPrefix(arg, "--- ") || strings.ContainsAny(arg, " \t")
}

// fixHintCommand is the command deja prints for the reader to paste, escaped so
// that it runs: a line starting with a dash needs the `--` in front of it, or
// the command deja just offered answers with "unknown flag" (#2799).
func fixHintCommand(line string) string {
	if strings.HasPrefix(line, "-") {
		return "deja fix -- " + pasteSafe(line)
	}
	return "deja fix " + pasteSafe(line)
}
