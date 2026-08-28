package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
)

// The terminal line and the JSON both distinguish "nothing indexed" from "the
// policy withholds everything indexed". The card told the reader to run `deja
// index`, which changes nothing in that state (#2288).
func TestTheCardDoesNotBlameAnEmptyIndexForThePolicy(t *testing.T) {
	// The premise: with nothing indexed at all, the old sentence is right.
	_, caption := heroStat(stats.Report{})
	if !strings.Contains(caption, "deja index") {
		t.Fatalf("an empty store no longer points at `deja index`: %q", caption)
	}

	_, caption = heroStat(stats.Report{PolicyWithheld: 3})
	if strings.Contains(caption, "run deja index") {
		t.Errorf("the card blames an empty index while the policy holds 3 sessions: %q", caption)
	}
	if !strings.Contains(caption, "polic") {
		t.Errorf("the card does not name what is withholding the sessions: %q", caption)
	}

	// A store with sessions is unaffected either way.
	if _, caption := heroStat(stats.Report{TotalSessions: 12}); !strings.Contains(caption, "searchable") {
		t.Errorf("a normal store lost its line: %q", caption)
	}
}
