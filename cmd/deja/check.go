package main

import (
	"fmt"
	"io"
)

// runCheck is the human-facing form of the plan hook. It deliberately calls
// planFindings directly so measured precision reflects the injected path.
// The empty session id skips the .hookseen dedupe on purpose: dedupe governs
// when a finding is delivered twice in one session, not whether it matches,
// and a measurement run has to see every match.
func runCheck(dir string, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) != 1 || args[0] != "-" {
		return fmt.Errorf("check needs '-' to read a plan from stdin")
	}
	plan, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("check: read plan: %w", err)
	}
	for _, finding := range planFindings(dir, string(plan), "") {
		fmt.Fprintln(stdout, finding)
	}
	return nil
}
