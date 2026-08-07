package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// injectionLifecycle words a recorded state for the block the agent reads. It
// mirrors lifecycleLine's vocabulary: "superseded" is our word, not something
// a model can act on.
func injectionLifecycle(state string) string {
	switch state {
	case lifecycleRejected:
		return "was tried and rejected"
	case lifecycleSuperseded:
		return "was replaced by a later decision"
	case lifecycleStale:
		return "was marked stale and may no longer hold"
	}
	return ""
}

// orderForInjection puts the sessions a person marked rejected last and
// returns the warning that has to travel with every marked session.
//
// recall attaches a correction to the session it belongs to (#684, #694). The
// two paths that inject unprompted did not: the session-start block listed the
// note as a separate item and left the session unmarked, and hook-prompt
// served a rejected session as an equal answer (#761). The hooks are what the
// agent reads before it does anything.
//
// Only rejected moves — superseded and stale are still the best record of how
// the current answer was reached, same as in search. But they have to be said:
// promoting a correction changed `deja search` and changed nothing in the
// block, so the replaced line kept leading the injection unmarked.
func orderForInjection(ss []model.Session) ([]model.Session, string) {
	states := sources.PromotedLifecycles()
	if len(states) == 0 {
		return ss, earlierAttemptWarning(ss)
	}
	marked := func(s model.Session) (sources.Lifecycle, string, bool) {
		lc, ok := states[s.Harness+":"+s.ID]
		if !ok {
			return lc, "", false
		}
		phrase := injectionLifecycle(lc.State)
		return lc, phrase, phrase != ""
	}
	rejected := func(s model.Session) bool {
		lc, ok := states[s.Harness+":"+s.ID]
		return ok && lc.State == lifecycleRejected
	}
	out := append([]model.Session(nil), ss...)
	sort.SliceStable(out, func(i, j int) bool {
		return !rejected(out[i]) && rejected(out[j])
	})
	var warn []string
	for _, s := range out {
		lc, phrase, ok := marked(s)
		if !ok {
			continue
		}
		// The id and the note are free text that travelled with the session.
		// This warning is one line of an injected digest, so a note spanning
		// several lines writes rows the store never held.
		line := fmt.Sprintf("session %s %s", recallListingLine(s.ID), phrase)
		if lc.Note != "" {
			line += ": " + recallListingLine(lc.Note)
		}
		warn = append(warn, line)
	}
	if len(warn) == 0 {
		return out, earlierAttemptWarning(out)
	}
	return out, "Some of this no longer holds — " + strings.Join(warn, "; ") + ".\n" + earlierAttemptWarning(out)
}

// earlierAttemptWarning names the injected sessions the project has already
// moved past. The search screen has carried this since #694; the block the
// agent reads did not, so two contradictory decisions from the same project
// arrived side by side with nothing but their order to tell them apart — and
// order is not a claim the reader can act on.
func earlierAttemptWarning(ss []model.Session) string {
	older := search.EarlierAttempts(ss)
	if len(older) == 0 {
		return ""
	}
	var lines []string
	for _, s := range ss {
		if when := older[s.ID]; when != "" {
			lines = append(lines, fmt.Sprintf("session %s is an earlier attempt — this project has a newer session on the same ground (%s)", s.ID, when))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "; ") + ".\n"
}
