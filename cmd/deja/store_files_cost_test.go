package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// `noAgentHistoryFound` reads one field of the inspection — `store.Files`,
// which is `len(check.files)` set at doctor_report.go:471 and never mutated —
// and pays a stat per path, a listing per directory, and the newest file of
// every store opened and run through that store's parser to get it. On a real
// home that measured 514 ms against 6.6 ms for the same answer (#1991).
//
// A ratio rather than a duration, so it means the same thing on a slow runner,
// and against the enumeration rather than the clock, so it does not become a
// flake when a machine is busy.
func TestTheEmptyScreenDoesNotParseEveryStore(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	// Files big enough that parsing them is not free: the sandbox is why this
	// cost was invisible to my first measurement, so the fixture has to put it
	// back.
	long := make([]byte, 4<<20)
	for i := range long {
		long[i] = 'x'
	}
	for i := 0; i < 4; i++ {
		seedClaude(t, claude, "app", string(rune('a'+i)), "the pgbouncer pool kept timing out "+string(long), "we retried")
	}

	// Best of three on each side. Both are well under a millisecond, and a
	// runner busy with the rest of the suite stretches such a sample by more
	// than the bar below allows — the same flake #2193 caught in the usage
	// package. The minimum is the run that was not interrupted.
	enumerate, asked := time.Duration(1<<62-1), time.Duration(1<<62-1)
	var checks []doctorStoreCheck
	for round := 0; round < 3; round++ {
		start := time.Now()
		checks = doctorStoreChecks()
		if took := time.Since(start); took < enumerate {
			enumerate = took
		}
		start = time.Now()
		answer := noAgentHistoryFound()
		if took := time.Since(start); took < asked {
			asked = took
		}
		if answer {
			t.Fatal("the fixture has history, so this measures nothing")
		}
	}
	// Four times the enumeration, plus a millisecond or two of slack for a
	// loaded runner: far above the spread between runs, far below the cost of
	// reading and parsing a store's newest file.
	if asked > 4*enumerate+2*time.Millisecond {
		t.Errorf("the question took %v against %v to enumerate the stores: it is parsing them", asked, enumerate)
	}
	t.Logf("enumerate %v, answer %v (%d stores)", enumerate, asked, len(checks))

	if _, err := os.Stat(filepath.Join(claude, "-app")); err != nil {
		t.Fatal(err)
	}
}
