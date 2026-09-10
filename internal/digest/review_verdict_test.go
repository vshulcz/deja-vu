package digest

import "testing"

// A review agent's report reads as a decision to every rule here — it is full of
// "approved", "fixed", "findings" — and it is about one diff on one day, which
// makes it the worst thing to carry forward. Read from the lines deja would have
// served before an edit on a real store: `index.tsx — No findings.` and, worst,
// `config.go — prior decision: Deploy: NO-DEPLOY until at least findings 1 and 2
// are fixed.` — a hold from a review of something else, offered months later as
// the standing position on that file.
func TestAReviewVerdictIsNotSomethingConcluded(t *testing.T) {
	reports := []string{
		"VERDICT: APPROVED ISSUES: - None. REQUIRED_FIXES: - None.",
		"VERDICT: NEEDS_CHANGES ISSUES: - the billing index re-exports a type twice",
		"Findings: none high/medium. Deploy: DEPLOY.",
		"No findings.",
		"Deploy: **NO-DEPLOY** until at least findings 1 and 2 are fixed.",
	}
	for _, r := range reports {
		if !IsAgentArtifact(r) {
			t.Errorf("a review report is treated as the session's own words:\n  %s", r)
		}
	}
}

// A sentence a person or an agent writes about the work is not a report, even
// where it uses the same vocabulary.
func TestARealConclusionAboutFindingsSurvives(t *testing.T) {
	keep := []string{
		"the retry budget stays at four; the review findings about backoff were already covered",
		"решили не деплоить по пятницам — откатывать некому",
		"we approved the migration plan and it is the one we keep",
	}
	for _, k := range keep {
		if IsAgentArtifact(k) {
			t.Errorf("a real sentence was dropped as a report:\n  %s", k)
		}
	}
}
