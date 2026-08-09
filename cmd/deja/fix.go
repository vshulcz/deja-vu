package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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
		default:
			// A flag deja does not know must not be swallowed into the error
			// text — `deja fix "..." --json` used to search for a string that
			// contained "--json" and answer "no session ran a command".
			if strings.HasPrefix(args[i], "-") && args[i] != "-" {
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
		fmt.Fprintln(stdout, "deja: no session on this machine ran a command after that error")
		return nil
	}
	for _, p := range pairs {
		when := ""
		if !p.When.IsZero() {
			when = " · " + p.When.Local().Format("2006-01-02")
		}
		fmt.Fprintf(stdout, "%s%s\n", search.SafeText(p.Error), when)
		fmt.Fprintf(stdout, "  ran next: %s\n", search.SafeText(p.Command))
	}
	return nil
}
