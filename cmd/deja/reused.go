package main

import (
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
	// The usage log records a bare session id (cmd/deja/mcp.go logs
	// h.Session.ID), while the manifest keys sessions by harness and id. So the
	// lookup is by id — and an id carried by two harnesses is ambiguous in a
	// way no tie-break can fix: the recall count under that id is the *sum* of
	// both sessions, so naming either one attaches a number that belongs to
	// two. Those ids are dropped rather than guessed. Latent on this store
	// (1153 sessions, 1153 distinct ids), but a duplicate makes the answer
	// flip between runs on identical data, because the metas come out of a map.
	byID := make(map[string][]index.SessionMeta, len(metas))
	for _, m := range metas {
		byID[m.ID] = append(byID[m.ID], m)
	}
	cutoff := time.Now().Add(-reusedSettleAge)
	var best reusedMemory
	var bestKey string
	for key, times := range worn {
		if times < reusedMinTimes {
			continue
		}
		found := byID[key]
		if len(found) != 1 {
			continue
		}
		m := found[0]
		if strings.TrimSpace(m.Title) == "" {
			continue
		}
		// A session with no parseable timestamp is not old, it is undated —
		// the zero time passes any cutoff and then prints as "Jan 1 0001".
		if m.Updated.IsZero() || !m.Updated.Before(cutoff) {
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
