package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The brief is the owner's own screen, and browsing is what the `search` rule
// governs — `recent` honours it. Two lines below it, `asked` and `hit` were
// filtered by `auto` instead, so a policy that keeps work out of casual greps
// while leaving auto-recall on printed "all N sessions are withheld from your
// own searches" and the text of those sessions underneath (#1312).
func TestBriefHonoursOneRuleAcrossTheWholeScreen(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {"why does the docker build fail on arm64", 40 * 24 * time.Hour},
		"a2": {"why does the docker build fail on arm64", 6 * 24 * time.Hour},
	})
	writePolicy(t, `{"activations":{"search":{"local":false},"auto":{"local":true}}}`)
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "search rule keeps them out of your own searches") {
		t.Fatalf("the screen does not report the rule under test:\n%s", out)
	}
	if strings.Contains(out, "docker build fail on arm64") {
		t.Errorf("session text printed under a line saying it is all withheld:\n%s", out)
	}
}

// The reverse policy — browsing allowed, auto-recall denied — must keep those
// lines, since nothing withholds them from the reader.
func TestBriefKeepsItsInsightsWhenOnlyTheAgentIsDenied(t *testing.T) {
	dir := seedAgedSessions(t, map[string]agedSession{
		"a1": {"why does the docker build fail on arm64", 40 * 24 * time.Hour},
		"a2": {"why does the docker build fail on arm64", 6 * 24 * time.Hour},
	})
	writePolicy(t, `{"activations":{"search":{"local":true},"auto":{"local":false}}}`)
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "asked      ") {
		t.Errorf("a rule that only stops the agent emptied the reader's own screen:\n%s", out)
	}
}

// The withheld line itself names whichever rule withholds everything, and it
// prefers auto; on this policy the rule that emptied the screen is search.
func TestBriefNamesTheRuleThatEmptiedTheScreen(t *testing.T) {
	storeWith(t, false)
	writePolicy(t, `{"activations":{"search":{"local":false},"auto":{"local":true}}}`)
	var buf bytes.Buffer
	if err := runBrief(index.DefaultDir(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "withheld") {
		t.Fatalf("no withheld line on a store the policy withholds:\n%s", out)
	}
	if !strings.Contains(out, "search rule") {
		t.Errorf("the line names a rule other than the one that emptied the screen:\n%s", out)
	}
	if strings.Contains(out, "recent ") {
		t.Errorf("sessions listed under a line saying they are all withheld:\n%s", out)
	}
}

// When two rules withhold everything, the reader is told the one that emptied
// the screen in front of them rather than the one they never see fail.
func TestBriefPrefersTheRuleItObeys(t *testing.T) {
	storeWith(t, false)
	writePolicy(t, `{"activations":{"search":{"local":false},"auto":{"local":false}}}`)
	var buf bytes.Buffer
	if err := runBrief(index.DefaultDir(), &buf); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "search rule keeps them out of your own searches") {
		t.Errorf("with both rules withholding, the screen names the one it does not obey:\n%s", out)
	}
}
