package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
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
	if len(ss) == 0 {
		return ss, ""
	}
	r := newLifecycleResolver(index.DefaultDir())
	marked := func(s model.Session) (string, string, bool) {
		state, note, _, _, ok := r.resolve(s)
		if !ok {
			return "", "", false
		}
		phrase := injectionLifecycle(state)
		return note, phrase, phrase != ""
	}
	rejected := func(s model.Session) bool {
		state, _, _, _, ok := r.resolve(s)
		return ok && state == lifecycleRejected
	}
	out := append([]model.Session(nil), ss...)
	sort.SliceStable(out, func(i, j int) bool {
		return !rejected(out[i]) && rejected(out[j])
	})
	var warn []string
	for _, s := range out {
		note, phrase, ok := marked(s)
		if !ok {
			continue
		}
		// The id and the note are free text that travelled with the session.
		// This warning is one line of an injected digest, so a note spanning
		// several lines writes rows the store never held.
		line := fmt.Sprintf("session %s %s", recallListingLine(s.ID), phrase)
		if note != "" {
			line += ": " + recallListingLine(note)
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
		when := older[s.Key()]
		if when == "" {
			when = older[s.ID]
		}
		if when != "" {
			// The id is free text from the transcript; sanitise it the way the
			// rejected-warning above does, or a crafted sessionId carries an
			// escape or an invisible run into the agent block this line joins.
			lines = append(lines, fmt.Sprintf("session %s is an earlier attempt — this project has a newer session on the same ground (%s)", recallListingLine(s.ID), when))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "; ") + ".\n"
}

// shownInjectionWarning computes the rejected/superseded and earlier-attempt
// warnings across all candidate sessions, but only emits lines for sessions
// actually present in shown. Without this filter, a 12-candidate pool whose
// digest only rendered 1–2 sessions warned about earlier-attempt session IDs
// that never appeared in the injected block.
func shownInjectionWarning(candidates, shown []model.Session) string {
	if len(candidates) == 0 || len(shown) == 0 {
		return ""
	}
	keep := make(map[string]bool, len(shown)*2)
	for _, s := range shown {
		if s.ID != "" {
			keep[s.ID] = true
		}
		if k := s.Key(); k != "" && k != ":" {
			keep[k] = true
		}
	}
	inShown := func(s model.Session) bool {
		return keep[s.ID] || keep[s.Key()]
	}
	r := newLifecycleResolver(index.DefaultDir())
	var warn []string
	for _, s := range candidates {
		if !inShown(s) {
			continue
		}
		state, note, _, _, ok := r.resolve(s)
		if !ok {
			continue
		}
		phrase := injectionLifecycle(state)
		if phrase == "" {
			continue
		}
		line := fmt.Sprintf("session %s %s", recallListingLine(s.ID), phrase)
		if note != "" {
			line += ": " + recallListingLine(note)
		}
		warn = append(warn, line)
	}
	older := search.EarlierAttempts(candidates)
	var earlier []string
	for _, s := range candidates {
		if !inShown(s) {
			continue
		}
		when := older[s.Key()]
		if when == "" {
			when = older[s.ID]
		}
		if when != "" {
			earlier = append(earlier, fmt.Sprintf("session %s is an earlier attempt — this project has a newer session on the same ground (%s)", recallListingLine(s.ID), when))
		}
	}
	var earlierText string
	if len(earlier) > 0 {
		earlierText = strings.Join(earlier, "; ") + ".\n"
	}
	if len(warn) == 0 {
		return earlierText
	}
	return "Some of this no longer holds — " + strings.Join(warn, "; ") + ".\n" + earlierText
}

func sessionsMatchingIDs(ss []model.Session, ids []string) []model.Session {
	if len(ids) == 0 {
		return nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []model.Session
	for _, s := range ss {
		if want[s.ID] || want[s.Key()] {
			out = append(out, s)
		}
	}
	return out
}
