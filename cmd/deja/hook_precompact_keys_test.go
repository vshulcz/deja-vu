package main

import (
	"os"
	"strings"
	"testing"
)

// A compaction takes the session-start digest off the screen along with
// everything else, and the seen list is what stops deja sending it again. The
// per-prompt rows were forgotten and the two the session-start hook writes —
// `start:<sid>` and the once mark — were not, so the next start refused to
// send back exactly what the compaction had just lost. On Kimi, whose only
// rows are those two, nothing was forgotten at all (#3307).
func TestCompactionForgetsEverySeenKeyOfTheSession(t *testing.T) {
	dir := t.TempDir() + "/index"
	const sid = "session_3f1f"
	rememberInjectedIDs(dir, sid, "block-fingerprint")
	rememberInjectedIDsFor(dir, sessionStartKeyPrefix+sid, "proj", []string{"other-session"})
	rememberSessionDigest(dir, sid)
	// Another session's rows, which this must leave alone.
	rememberInjectedIDsFor(dir, sessionStartKeyPrefix+"session_other", "proj", []string{"kept-session"})
	rememberSessionDigest(dir, "session_other")

	before, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{sid + " block-fingerprint", sessionStartKeyPrefix + sid, onceDigestKey(sid)} {
		if !strings.Contains(string(before), want) {
			t.Fatalf("the fixture never wrote %q, so this measures nothing:\n%s", want, before)
		}
	}

	forgetInjected(dir, sid)

	after, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{sid + " block-fingerprint", sessionStartKeyPrefix + sid + " ", onceDigestKey(sid) + " "} {
		if strings.Contains(string(after), gone) {
			t.Errorf("%q survived the compaction:\n%s", gone, after)
		}
	}
	for _, kept := range []string{sessionStartKeyPrefix + "session_other", onceDigestKey("session_other")} {
		if !strings.Contains(string(after), kept) {
			t.Errorf("another session lost %q:\n%s", kept, after)
		}
	}
}
