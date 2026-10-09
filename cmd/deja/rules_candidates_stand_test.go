package main

import (
	"strings"
	"testing"
	"time"
)

// The review stand's two corrections came back as "no corrections found". The
// detector reads both phrasings; the stand lived under /tmp, which the
// candidate list drops as a test run, and the empty answer did not say so.
func TestRulesCandidatesReadsPlainCorrectionsAndNamesSkippedStands(t *testing.T) {
	candidateEnv(t)
	seedCandidateSession(t, "-home-u-checkout", "ci1", t0,
		candidateTurn{"user", "the pool tests fail on CI with connection refused, fix it"},
		candidateTurn{"assistant", "I added docker compose up -d postgres to the CI job."},
		candidateTurn{"user", "no, don't use docker compose in CI, use the service container from the workflow"},
	)
	seedCandidateSession(t, "-home-u-checkout", "lint1", t0.Add(time.Hour),
		candidateTurn{"user", "pool tests are red again locally"},
		candidateTurn{"assistant", "Fixed, and ran make lint again."},
		candidateTurn{"user", "stop running make lint after every edit, only before commit"},
	)
	if got := runCandidatesJSON(t); len(got) != 2 {
		t.Fatalf("candidates = %+v, want both corrections", got)
	}

	candidateTempRoots = func() []string { return []string{"/tmp", "/private/tmp"} }
	seedCandidateSession(t, "-tmp-stand-checkout", "stand1", t0.Add(2*time.Hour),
		candidateTurn{"user", "run the probe"},
		candidateTurn{"assistant", "done"},
		candidateTurn{"user", "no, run it with the seed"},
	)
	out, err := captureRun(t, "rules", "candidates", "--since", "1d")
	if err != nil {
		t.Fatal(err)
	}
	// The count is not pinned: on Linux t.TempDir is under /tmp too, so the
	// two sessions above are stands as well.
	if !strings.Contains(out, "in the last 1d") || !strings.Contains(out, "run from a temporary directory left out") {
		t.Fatalf("the empty answer does not say what it skipped:\n%s", out)
	}
}
