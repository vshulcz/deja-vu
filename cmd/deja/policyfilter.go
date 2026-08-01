package main

import (
	"fmt"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// policyFilterHits drops search hits the trust policy blocks on this
// activation path. The default policy blocks nothing.
func policyFilterHits(activation string, hits []search.Hit) []search.Hit {
	kept, _ := policyFilterHitsCounted(activation, hits)
	return kept
}

// policyFilterHitsCounted also reports how many hits the policy took away.
//
// Without the count, "nothing matched" and "a rule hides it on this path" are
// the same empty answer — and only the second one is something the reader can
// act on (#680).
func policyFilterHitsCounted(activation string, hits []search.Hit) ([]search.Hit, int) {
	before := len(hits)
	kept := policy.Filter(policy.Load(), activation, hits, func(h search.Hit) string {
		return h.Session.Project
	})
	return kept, before - len(kept)
}

// policyHiddenNote is the one line that names the policy when it is the reason
// an answer is empty.
func policyHiddenNote(activation string, hidden int) string {
	if hidden <= 0 {
		return ""
	}
	return fmt.Sprintf("deja: the trust policy hides %d matching session%s on this path (%s: %s) — see %s\n",
		hidden, pluralS(hidden), activation, policy.Load().Describe(activation), policy.Path())
}

// policyFilterBlame is policyFilterHits for blame results, which carry whole
// sessions rather than budgeted snippets — the path that most needs the rule.
func policyFilterBlame(activation string, hits []search.BlameHit) []search.BlameHit {
	return policy.Filter(policy.Load(), activation, hits, func(h search.BlameHit) string {
		return h.Session.Project
	})
}
