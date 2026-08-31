package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// recall_context returns the most text of any tool — a whole session — and
// said what it was in one bracketed word. `[relevance]` above a session that
// matched nothing reads as a label on an answer, where recall says it in a
// sentence (#2787, the shape #2074 fixed for the counted page).
func TestTheContextLeadSaysWhatTheSessionIs(t *testing.T) {
	lead := contextTierLead(search.TierRelevance)
	if strings.HasPrefix(lead, "[") {
		t.Errorf("a whole session is still introduced by a marker: %q", lead)
	}
	for _, want := range []string{"No session is about this", "nearest by wording", "not as a record"} {
		if !strings.Contains(lead, want) {
			t.Errorf("the lead does not say %q: %q", want, lead)
		}
	}
}

// The other tiers keep their marker: they did match, and the marker says how.
func TestTheOtherTiersKeepTheirMarker(t *testing.T) {
	for _, tier := range []string{search.TierError, search.TierSemantic, "close"} {
		lead := contextTierLead(tier)
		if !strings.HasPrefix(lead, "["+tier+"]") {
			t.Errorf("%s lost its marker: %q", tier, lead)
		}
	}
}
