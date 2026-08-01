package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const lifecycleRejected = "rejected"

// attachLifecycles marks hits whose decision was later rejected, superseded or
// left to go stale.
//
// deja recorded this already: `deja promote <id> --state rejected` writes the
// state and the correction, and prints that the note "now outranks the raw
// transcript in recall". Measured, it did not — the correction only surfaced
// when its own wording matched the query, so asking about a reverted decision
// returned the transcript that made it, reading like current truth. The state
// belongs to the session; ranking was never going to carry it.
func attachLifecycles(hits []search.Hit) {
	if len(hits) == 0 {
		return
	}
	states := sources.PromotedLifecycles()
	if len(states) == 0 {
		return
	}
	for i := range hits {
		h := &hits[i]
		key := h.Session.Harness + ":" + h.Session.ID
		lc, ok := states[key]
		if !ok || lc.State == "" || lc.State == "accepted" {
			continue
		}
		h.Lifecycle = lc.State
		h.LifecycleNote = lc.Note
		if !lc.At.IsZero() {
			h.LifecycleAt = lc.At.Format("2006-01-02")
		}
	}
}

// lifecycleLine is what a reader — human or model — is told about a hit whose
// decision did not hold. The wording says what happened rather than naming the
// state, because "superseded" means nothing to someone who has not read our
// docs, and the model has to act on it without asking.
func lifecycleLine(h search.Hit) string {
	if h.Lifecycle == "" {
		return ""
	}
	var b strings.Builder
	switch h.Lifecycle {
	case "rejected":
		b.WriteString("[this was tried and rejected")
	case "superseded":
		b.WriteString("[a later decision replaced this")
	case "stale":
		b.WriteString("[marked stale — may no longer hold")
	default:
		b.WriteString("[" + h.Lifecycle)
	}
	if h.LifecycleAt != "" {
		fmt.Fprintf(&b, ", %s", h.LifecycleAt)
	}
	b.WriteString("]")
	if h.LifecycleNote != "" {
		b.WriteString(" " + h.LifecycleNote)
	}
	return b.String()
}

// demoteRejected moves the attempts a person marked rejected below the ones
// they did not, and drops the "earlier attempt" label from a hit whose newer
// rival is one of them.
//
// rejected is the strongest statement someone can make about a piece of their
// own history, and it used to move nothing: the wrong answer stayed first
// while the decision that replaced it was labelled as superseded *by* the
// rejected one, so an agent reading top-down got the wrong answer with a
// recommendation attached (#684).
//
// Only rejected demotes. superseded and stale say "this was true once", which
// is still the best record of how the current answer was reached; rejected
// says "this did not work".
func demoteRejected(hits []search.Hit) {
	var rejected []search.Hit
	for _, h := range hits {
		if h.Lifecycle == lifecycleRejected {
			rejected = append(rejected, h)
		}
	}
	if len(rejected) == 0 {
		return
	}
	for i := range hits {
		h := &hits[i]
		if h.Superseded == "" || h.Lifecycle == lifecycleRejected {
			continue
		}
		// The label carries a date, not an identity, so the rival is the
		// rejected hit in the same project stamped that day.
		for _, r := range rejected {
			if r.Session.Project == h.Session.Project && r.Session.Updated.Format("2006-01-02") == h.Superseded {
				h.Superseded = ""
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].Lifecycle != lifecycleRejected && hits[j].Lifecycle == lifecycleRejected
	})
}
