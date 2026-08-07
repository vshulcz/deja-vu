package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

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
	exists, _, err := policy.Diagnose()
	if !exists || err == nil {
		return
	}
	fmt.Fprintf(w, "deja: warning the trust policy at %s %s — every origin activates until it loads\n",
		policy.Path(), policyFailureReason(err))
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
