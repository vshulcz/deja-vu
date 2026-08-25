package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A peer's stamps are written by deja itself, so a copy from a machine a few
// hundred milliseconds ahead is not a broken clock; a session's stamps come
// from a transcript on some other machine, where anything ahead is unusable.
// Two rules, on purpose (#1855, #1753) — this pins the band where they differ,
// so a change to either is a change to a test rather than a silent one.
func TestThePeerRuleAndTheSessionRuleDifferByTheSlack(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	inside := now.Add(peerClockSlack / 2)
	outside := now.Add(peerClockSlack + time.Second)

	if peerStampedAhead(inside, now) {
		t.Errorf("a peer stamped inside the slack is flagged, so a clock a moment out reads as broken")
	}
	if !peerStampedAhead(outside, now) {
		t.Errorf("a peer stamped past the slack is not flagged")
	}
	if !index.StampedAhead(inside, now) {
		t.Errorf("the session rule has grown a tolerance, so the two rules no longer differ and the doc below is describing nothing")
	}
}

// The field is part of a documented contract, and the doc said the session
// rule, which is the one the peer surface does not use: someone implementing
// against it disagreed with deja over the whole slack band (#1865).
func TestTheDocumentedPeerAheadRuleNamesTheSlack(t *testing.T) {
	src, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(src)
	i := strings.Index(doc, "`stamped_ahead`")
	if i < 0 {
		t.Fatal("docs/json-output.md no longer describes stamped_ahead")
	}
	para := doc[i:]
	if end := strings.Index(para, "\n\n"); end > 0 {
		para = para[:end]
	}
	if !strings.Contains(para, "minute") {
		t.Errorf("the documented rule does not name the minute of slack the peer surface applies, so a reader implements the session rule:\n%s", para)
	}
	if strings.Contains(para, "the rule recall applies to sessions stamped ahead") {
		t.Errorf("the doc still says a peer is judged by the session rule:\n%s", para)
	}
}
