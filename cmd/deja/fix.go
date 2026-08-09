package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
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
		if args[i] == "--limit" && i+1 < len(args) {
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("fix: --limit wants a positive number, got %q", args[i])
			}
			limit = n
			continue
		}
		parts = append(parts, args[i])
	}
	text := strings.TrimSpace(strings.Join(parts, " "))
	if text == "" {
		// A pasted trace is many lines, and shells mangle those as arguments.
		b, err := io.ReadAll(os.Stdin)
		if err == nil {
			text = strings.TrimSpace(string(b))
		}
	}
	if text == "" {
		return fmt.Errorf("fix: give the error text, or pipe it in")
	}
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return ensureError(dir, err)
	}
	pairs := index.FixesFor(dir, text, limit)
	if len(pairs) == 0 {
		// Saying why costs nothing and stops the next question: an error deja
		// does not recognise as an error is a different miss from one it has
		// simply never seen.
		if !index.LooksLikeError(text) {
			fmt.Fprintln(stdout, "deja: that does not read like an error line — pass the failing output itself")
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
		fmt.Fprintf(stdout, "%s%s\n", search.SafeText(p.Error), when)
		fmt.Fprintf(stdout, "  ran next: %s\n", search.SafeText(p.Command))
	}
	return nil
}
