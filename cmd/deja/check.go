package main

import (
	"fmt"
	"io"
	"os"
)

// runCheck is the human-facing form of the plan hook. It deliberately calls
// planFindings directly so measured precision reflects the injected path.
// The empty session id skips the .hookseen dedupe on purpose: dedupe governs
// when a finding is delivered twice in one session, not whether it matches,
// and a measurement run has to see every match.
func runCheck(dir string, args []string, stdin io.Reader, stdout io.Writer) error {
	return runCheckTo(dir, args, stdin, stdout, os.Stderr)
}

// runCheckTo is runCheck with somewhere to say why the answer is empty. The
// hook this shares a body with must stay silent — a PreToolUse hook that talks
// when it has nothing is noise on every plan — but the command a person typed
// owes them the difference between "looked and found nothing", "there was
// nothing in the plan to look for" and "there is no index to look in" (#2564).
// Findings keep stdout to themselves, so a measurement run parses it unchanged.
func runCheckTo(dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) != 1 || args[0] != "-" {
		return fmt.Errorf("check needs '-' to read a plan from stdin")
	}
	plan, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("check: read plan: %w", err)
	}
	findings := planFindings(dir, string(plan), "")
	for _, finding := range findings {
		fmt.Fprintln(stdout, finding)
	}
	if len(findings) > 0 {
		return nil
	}
	switch {
	case len(planSearchSteps(string(plan))) == 0:
		fmt.Fprintln(stderr, "deja: nothing in this plan to check — it needs a step naming what it will do")
	case !planIndexReady(dir):
		fmt.Fprintln(stderr, "deja: no index to check against yet — `deja index` builds one")
	default:
		fmt.Fprintln(stderr, "deja: nothing found for this plan")
	}
	return nil
}
