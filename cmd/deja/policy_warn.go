package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// warnBrokenPolicy names a trust policy file that exists and does not load.
// Loading fails open, so the rule that was meant to keep imported memory out
// of a path stops holding — and the only place that said so was `doctor`,
// which is not where anyone looks while recall is answering (#1088).
func warnBrokenPolicy(cmd string, w io.Writer) {
	switch cmd {
	// doctor reports the same file in full, and these two never consult it.
	case "doctor", "version", "completion":
		return
	}
	exists, unknown, err := policy.Diagnose()
	if !exists {
		return
	}
	if err != nil {
		fmt.Fprintf(w, "deja: warning the trust policy at %s %s — every origin activates until it loads\n",
			policy.Path(), policyFailureReason(err))
		return
	}
	// A rule that parses and names something deja never consults fails open
	// exactly like a file that will not parse: the project the reader meant to
	// withhold is in every answer, and the file on disk reads like a
	// restriction. doctor listed these; nobody reads doctor while recall is
	// answering, which is the reason the warning above exists (#2452).
	if len(unknown) > 0 {
		fmt.Fprintf(w, "deja: warning the trust policy at %s names %s, which deja does not consult — %s does nothing and every origin activates\n",
			policy.Path(), quotedList(unknown, 3), pluralThat(len(unknown)))
	}
}

// quotedList names at most n of the keys, and says how many more there are.
func quotedList(keys []string, n int) string {
	if len(keys) <= n {
		out := make([]string, len(keys))
		for i, k := range keys {
			out[i] = fmt.Sprintf("%q", k)
		}
		return strings.Join(out, ", ")
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("%q", keys[i])
	}
	return fmt.Sprintf("%s and %d more", strings.Join(out, ", "), len(keys)-n)
}

func pluralThat(n int) string {
	if n == 1 {
		return "that rule"
	}
	return "those rules"
}

// policyFailureReason turns the load failure into a cause with something to do
// about it. The raw form is `open …/policy.json: permission denied`, which
// repeats the path and names a syscall instead of the fix.
func policyFailureReason(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case os.IsPermission(err):
		return "cannot be read (check its permissions)"
	case errors.As(err, &syn) || errors.As(err, &typ):
		return "is not valid JSON"
	default:
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return "cannot be read (" + pe.Err.Error() + ")"
		}
		return "cannot be read (" + err.Error() + ")"
	}
}
