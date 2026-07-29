package main

import (
	"fmt"
	"strings"

	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

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
