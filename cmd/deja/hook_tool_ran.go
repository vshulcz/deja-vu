package main

import (
	"fmt"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// An agent about to edit a file is told what was decided about it, and then it
// has to work out for itself how to check the edit — which on a repository of
// any size is the expensive part: measured on a 325-file fixture, ten to
// sixteen reads went into finding the command that runs the suite for one
// package, after deja had already said what the code does.
//
// The sessions that touched the file ran that command, and sessionfacts.gob
// records which of them the transcript saw pass. So the moment the file is
// opened is the moment to hand it over — the same join the file line already
// makes, one table further.
const (
	// toolHookMinRanSessions is the bar. One session running something is a
	// keystroke; two that both touched this file and both ended cleanly on the
	// same command is the project's way of checking it.
	toolHookMinRanSessions = 2
	// ranLabel is what the line says it is handing over. Not "the way to test
	// this" — a command that passed in two sessions is evidence, and the
	// sentence claims exactly that much.
	ranLabel = " — ran here and passed: "
	// toolHookRanMax is where a command stops being one and starts being a
	// paragraph in a line the agent reads on every action.
	toolHookRanMax = 90
)

// ranCandidate is one command that passed in the sessions that touched a file.
type ranCandidate struct {
	text     string
	sessions int
	last     time.Time
	// rank is how near the end of a session this command ran, best over the
	// sessions holding it: the entry keeps them most recent first, so a low
	// rank is a command a session ended on rather than one it moved past.
	rank int
}

// ranBetter orders two candidates: the one more sessions ran, then the one
// nearer the end of a session, then the more recent, then the text. The last
// two are there so the line is the same on every call — `make test` and the
// command that finally made it pass are usually in the same two sessions, and
// a map's order would have decided which one the agent was handed.
func ranBetter(a, b ranCandidate) bool {
	switch {
	case a.sessions != b.sessions:
		return a.sessions > b.sessions
	case a.rank != b.rank:
		return a.rank < b.rank
	case !a.last.Equal(b.last):
		return a.last.After(b.last)
	}
	return a.text < b.text
}

// fileHookRanLine is the command the sessions that touched this file ran and
// the transcript saw finish cleanly, or "" when there is no such pattern.
//
// Reads one sidecar and the manifest rows the caller already has: this is on
// the per-action path, where the file line's own history cost 133 ms before
// #3605 moved it into the manifest.
func fileHookRanLine(dir string, metas []index.SessionMeta) string {
	if len(metas) < toolHookMinRanSessions {
		return ""
	}
	facts := index.ReadSessionFacts(dir)
	if len(facts) == 0 {
		return ""
	}
	by := map[string]*ranCandidate{}
	// One row per session, whatever the caller handed over: the manifest is
	// matched on a basename, so a session that touched two files of the same
	// name arrives twice, and the number in the line is sessions.
	counted := map[string]bool{}
	for _, m := range metas {
		key := m.Harness + ":" + m.ID
		if counted[key] {
			continue
		}
		counted[key] = true
		f, ok := facts[key]
		if !ok {
			continue
		}
		// One session counts once for a command however often it ran it: the
		// number in the line is sessions, and a loop of forty retries is one
		// session's opinion.
		seen := map[string]bool{}
		for pos, c := range f.Commands {
			if !c.Passed() {
				continue
			}
			// The same exclusion the command line makes: `git status` passing
			// in three sessions is true and says nothing about this file.
			cmd := orientCommand(c.Text)
			if cmd == "" || index.InspectionCommand(cmd) {
				continue
			}
			ck := normalizedCommandText(cmd)
			if ck == "" || seen[ck] {
				continue
			}
			seen[ck] = true
			a := by[ck]
			if a == nil {
				a = &ranCandidate{text: cmd, rank: pos}
				by[ck] = a
			}
			a.sessions++
			if pos < a.rank {
				a.rank = pos
			}
			if m.Updated.After(a.last) {
				a.last = m.Updated
			}
		}
	}
	var best *ranCandidate
	for _, a := range by {
		if a.sessions < toolHookMinRanSessions {
			continue
		}
		if best == nil || ranBetter(*a, *best) {
			best = a
		}
	}
	if best == nil {
		return ""
	}
	// SafeCommand, not the quoting a pasted value gets: this is handed over to
	// be run, and wrapping it in quotes makes the whole line one word. What it
	// does strip is what a terminal would obey, and the newline that would end
	// deja's own sentence and start one that reads as deja speaking to the
	// agent (#1863). The spacing inside it is kept, because `-run "Pool  Size"`
	// is a different test filter from `-run "Pool Size"` (#2052).
	cmd := truncateToolLine(search.SafeCommand(best.text), toolHookRanMax)
	return fmt.Sprintf("%s`%s` (%s)", ranLabel, cmd, toolSessionCount(best.sessions))
}
