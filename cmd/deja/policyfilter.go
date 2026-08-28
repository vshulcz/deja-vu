package main

import (
	"fmt"
	"io"

	"github.com/vshulcz/deja-vu/internal/model"
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

// policyFilterSessionsCounted is the same gate for paths that carry sessions
// rather than hits — the listing, whose whole output is titles (#937).
func policyFilterSessionsCounted(activation string, ss []model.Session) ([]model.Session, int) {
	before := len(ss)
	kept := policy.Filter(policy.Load(), activation, ss, func(s model.Session) string {
		return s.Project
	})
	return kept, before - len(kept)
}

// policyHiddenProjects names the projects a rule is withholding right now. The
// view page needs them by name rather than by session: a stored digest carries
// no project field, so the only way to keep withheld content off a shareable
// page is to recognise the names inside it (#2315).
func policyHiddenProjects(activation string, ss []model.Session) map[string]bool {
	p := policy.Load()
	hidden := map[string]bool{}
	for _, s := range ss {
		if s.Project != "" && !p.Allows(activation, s.Project) {
			hidden[s.Project] = true
		}
	}
	return hidden
}

// denyPolicyHidden stops a direct-access command (show, share, handoff) from
// revealing a session a trust rule withholds. Naming an exact id is still
// browsing under the search activation — ctx already refuses here (#1026), and
// last and search never surface the session at all — so a peer's content a rule
// hides must not leak through a command that happens to take an id. Returns a
// non-nil error to return, or nil when the session is allowed.
func denyPolicyHidden(id string, s model.Session, w io.Writer) error {
	if kept, hidden := policyFilterSessionsCounted(policy.ActivationSearch, []model.Session{s}); len(kept) == 0 {
		fmt.Fprint(w, policyHiddenNote(policy.ActivationSearch, hidden))
		return fmt.Errorf("no session matches %q", id)
	}
	return nil
}
