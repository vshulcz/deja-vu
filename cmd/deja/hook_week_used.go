package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The week note's count says deja did something; it does not say what (#4615).
// "Used" is the agent's own word for it: a reply that opened with the credit
// line, "déjà vu: … (deja:<id>)", in a turn written this week. Without one,
// the sessions agents recalled twice or more this week stand in, the rule
// findReusedMemory reads for the receipt.

// weekUsedMaxRefs bounds the distinct ids a week's credit lines are resolved
// against the manifest. A week of real credits is a handful; a session about
// deja that quotes ids in examples should not turn the walk into a scan.
const weekUsedMaxRefs = 64

// weekUsedTitleMin is the narrowest the first title is quoted at when a
// second session's id shares the line; below it the second is left out.
const weekUsedTitleMin = 32

type weekUsedSession struct {
	meta  index.SessionMeta
	times int
	last  time.Time
}

// weekUsed returns the sessions credited aloud this week, most first, and
// whether they were credited; failing that, the ones recalled twice or more.
func weekUsed(dir string, now time.Time) ([]weekUsedSession, bool) {
	pol := policy.Load()
	allow := func(project string) bool { return pol.Allows(policy.ActivationSearch, project) }
	metas, err := index.AllMeta(dir)
	if err != nil || len(metas) == 0 {
		return nil, false
	}
	shown := func(m index.SessionMeta) bool {
		return allow(m.Project) && !pol.Ignored(m.Path, m.Project) && !strings.ContainsFunc(m.ID, isSpaceOrControl)
	}
	cut := usage.WeekCut(now)

	// A credit names a prefix; it counts only when the prefix is one session.
	// Two matches fail closed rather than guess, and so does a session the
	// policy keeps off this screen.
	resolved := map[string]*index.SessionMeta{}
	resolve := func(ref string) *index.SessionMeta {
		if m, ok := resolved[ref]; ok {
			return m
		}
		if len(resolved) >= weekUsedMaxRefs {
			return nil
		}
		var hit *index.SessionMeta
		n := 0
		for i := range metas {
			if strings.HasPrefix(metas[i].ID, ref) {
				n++
				hit = &metas[i]
			}
		}
		if n != 1 || !shown(*hit) {
			hit = nil
		}
		resolved[ref] = hit
		return hit
	}
	byKey := map[string]*weekUsedSession{}
	_ = index.EachAssistantTurnSince(dir, cut, allow, func(speaker index.SessionMeta, text string, at time.Time) {
		if at.After(now) {
			return
		}
		for _, ref := range stats.CreditedRefs(text) {
			m := resolve(ref)
			if m == nil || (m.Harness == speaker.Harness && m.ID == speaker.ID) {
				continue
			}
			k := m.Harness + ":" + m.ID
			u := byKey[k]
			if u == nil {
				u = &weekUsedSession{meta: *m}
				byKey[k] = u
			}
			u.times++
			if at.After(u.last) {
				u.last = at
			}
		}
	})
	if len(byKey) > 0 {
		return sortWeekUsed(byKey), true
	}

	// The usage log names a bare id; one carried by two harnesses is dropped,
	// as findReusedMemory drops it. Today's sessions stay out for its reason
	// too: the most-recalled session is often the one still being worked on.
	worn := usage.WornSessionsSince(dir, cut)
	if len(worn) == 0 {
		return nil, false
	}
	byID := map[string][]int{}
	for i := range metas {
		byID[metas[i].ID] = append(byID[metas[i].ID], i)
	}
	settle := now.Add(-reusedSettleAge)
	for id, times := range worn {
		if times < reusedMinTimes || len(byID[id]) != 1 {
			continue
		}
		m := metas[byID[id][0]]
		if !shown(m) || m.Updated.IsZero() || !m.Updated.Before(settle) {
			continue
		}
		byKey[m.Harness+":"+m.ID] = &weekUsedSession{meta: m, times: times, last: m.Updated}
	}
	return sortWeekUsed(byKey), false
}

func sortWeekUsed(byKey map[string]*weekUsedSession) []weekUsedSession {
	out := make([]weekUsedSession, 0, len(byKey))
	for _, u := range byKey {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].times != out[j].times {
			return out[i].times > out[j].times
		}
		if !out[i].last.Equal(out[j].last) {
			return out[i].last.After(out[j].last)
		}
		return out[i].meta.Harness+":"+out[i].meta.ID < out[j].meta.Harness+":"+out[j].meta.ID
	})
	return out
}

func isSpaceOrControl(r rune) bool { return r <= ' ' || r == 0x7f }

// weekUsedLine is the week note's second line, or "" when no session earns it.
//
//	agents used "fix the JWT clock skew in…" (claude) — deja show e8b6eefc-fec (also 7a1c2d3e-5f6)
//
// A shared reader gets "agents used a claude session — deja show <id>".
func weekUsedLine(dir string, now time.Time, private bool) string {
	used, credited := weekUsed(dir, now)
	if len(used) == 0 {
		return ""
	}
	verb, times := "agents used ", ""
	if !credited {
		verb, times = "agents recalled ", fmt.Sprintf(" %d times", used[0].times)
	}
	first := used[0]
	plain := verb + weekUsedAgent(first.meta) + times + " — deja show " + weekUsedID(dir, first.meta)
	if termwidth.Columns(plain) > noteWidth {
		// An id too long for the line is one nobody types; say nothing.
		return ""
	}
	title := weekUsedTitle(first.meta)
	if !private || title == "" {
		return plain
	}
	tail := times + weekUsedHarness(first.meta) + " — deja show " + weekUsedID(dir, first.meta)
	// A second credited session rides along as its id, while the first title
	// keeps enough room to be read.
	if credited && len(used) > 1 {
		also := " (also " + weekUsedID(dir, used[1].meta) + ")"
		if noteWidth-termwidth.Columns(verb+tail+also) >= weekUsedTitleMin {
			tail += also
		}
	}
	room := noteWidth - termwidth.Columns(verb+tail)
	if room < weekUsedTitleMin/2 {
		return plain
	}
	return verb + quoteWithin(title, room) + tail
}

func weekUsedTitle(m index.SessionMeta) string { return trimBriefTitleTo(m.Title, 1<<20) }

// weekUsedAgent is "a claude session", or "a session" when none is named.
func weekUsedAgent(m index.SessionMeta) string {
	switch {
	case m.Harness == "":
		return "a session"
	case strings.ContainsRune("aeiou", rune(m.Harness[0])):
		return "an " + m.Harness + " session"
	}
	return "a " + m.Harness + " session"
}

// weekUsedHarness is " (claude)", or "" when the session names no agent.
func weekUsedHarness(m index.SessionMeta) string {
	if m.Harness == "" {
		return ""
	}
	return " (" + m.Harness + ")"
}

// weekUsedID is the id as `deja show` takes it: the short prefix deja prints
// everywhere, or the whole id when that prefix names more than one session.
func weekUsedID(dir string, m index.SessionMeta) string {
	short := shortID(m.ID)
	// Counted the way show resolves it: a session the policy withholds
	// neither takes the short id nor gets named by a longer one (#2401).
	if short != m.ID && index.PrefixMatchesAllowed(dir, short, searchAllows()) > 1 {
		return m.ID
	}
	return short
}
