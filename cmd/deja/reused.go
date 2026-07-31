package main

import (
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// "75 recalls" is a rate. It says deja did something, seventy-five times, and
// a reader has no way to check it or repeat it to anyone. What #579 asks for
// is the other half: name the thing that kept being worth recalling.
//
// The data is already recorded — every served recall logs which sessions it
// returned — so this is a naming problem, not a counting one, and
// `deja stats --impact` stays the source of truth for the arithmetic.
const (
	// reusedMinTimes is the floor for calling something re-used. Twice is the
	// point where a session stopped being a one-off answer.
	reusedMinTimes = 2
	// reusedSettleAge keeps today's work out of it. Measured while building
	// this: the most-served session on this machine was the session doing the
	// building, at 67 recalls — a mirror, not a memory. A session has to have
	// survived a day before its reuse says anything.
	reusedSettleAge = 24 * time.Hour
)

// reusedMemory is one session agents kept coming back to.
type reusedMemory struct {
	Title string
	Times int
	Age   time.Time
}

// findReusedMemory picks the session agents recalled most, ignoring the ones
// still being worked on. Usage log plus manifest: no record read.
func findReusedMemory(dir string) (reusedMemory, bool) {
	worn := usage.WornSessions(dir)
	if len(worn) == 0 {
		return reusedMemory{}, false
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return reusedMemory{}, false
	}
	// The usage log keys sessions by id alone, while the manifest keys them by
	// harness and id, so both forms have to resolve.
	byKey := make(map[string]index.SessionMeta, len(metas)*2)
	for _, m := range metas {
		byKey[m.Harness+":"+m.ID] = m
		byKey[m.ID] = m
	}
	cutoff := time.Now().Add(-reusedSettleAge)
	var best reusedMemory
	var bestKey string
	for key, times := range worn {
		if times < reusedMinTimes {
			continue
		}
		m, ok := byKey[key]
		if !ok || strings.TrimSpace(m.Title) == "" {
			continue
		}
		if !m.Updated.Before(cutoff) {
			continue
		}
		if times > best.Times || (times == best.Times && key < bestKey) {
			best = reusedMemory{Title: m.Title, Times: times, Age: m.Updated}
			bestKey = key
		}
	}
	if best.Times < reusedMinTimes {
		return reusedMemory{}, false
	}
	return best, true
}

// reusedTitles are the sessions worth naming, most-reused first — used where
// several fit rather than one.
func reusedTitles(dir string, n int) []reusedMemory {
	worn := usage.WornSessions(dir)
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil
	}
	byKey := make(map[string]index.SessionMeta, len(metas)*2)
	for _, m := range metas {
		byKey[m.Harness+":"+m.ID] = m
		byKey[m.ID] = m
	}
	cutoff := time.Now().Add(-reusedSettleAge)
	var out []reusedMemory
	seen := map[string]bool{}
	for key, times := range worn {
		m, ok := byKey[key]
		if times < reusedMinTimes || !ok || strings.TrimSpace(m.Title) == "" {
			continue
		}
		if !m.Updated.Before(cutoff) || seen[m.Title] {
			continue
		}
		seen[m.Title] = true
		out = append(out, reusedMemory{Title: m.Title, Times: times, Age: m.Updated})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Times != out[j].Times {
			return out[i].Times > out[j].Times
		}
		return out[i].Title < out[j].Title
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
