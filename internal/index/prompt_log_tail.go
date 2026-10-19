package index

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// promptLogTails names, for each prompt log in a replacement pass that only
// grew since the last one, the session ids its new lines carry. Such a log is
// read the way a shared store is read from its watermark: the sessions with a
// new line are replaced, each read whole, and the rest keep their records.
// Reading the log whole replaced every session known from it alone, 250 of
// 258 for one new prompt (#5403).
//
// Only a log whose prefix is what the last pass read: Codex trims
// history.jsonl from the front past max_bytes, and that one is read whole. So
// is one whose clip count is not split by session, which a pass that reads a
// few of them again could not keep right. A log that did not change at all,
// read again because a transcript beside it came or went, has no tail.
func promptLogTails(changed, old map[string]FileState, ingest map[string]FileIngest) map[string]map[string]bool {
	var out map[string]map[string]bool
	for p, f := range changed {
		of, known := old[p]
		if !known || !isCodexHistory(p) || !clipsBySession(ingest[p]) {
			continue
		}
		ids := map[string]bool{}
		if !sameFile(of, f) {
			if !canAppendIncremental(map[string]FileState{p: f}, old) {
				continue
			}
			var err error
			if ids, err = sources.PromptLogSessionIDs(p, resumeOffset(of), f.Size); err != nil {
				continue
			}
		}
		if out == nil {
			out = map[string]map[string]bool{}
		}
		out[p] = ids
	}
	return out
}

// rereadPromptLogs puts the prompt logs into a pass that removes a transcript
// or brings one for a session held from a log. The log parser skips a session
// whose transcript is on disk, so either can move a session into the parse or
// out of it with no new line in the log, and a pass that left the log alone
// kept what a rebuild drops, or missed what it reads. Read from their tail,
// the logs cost a parse and replace only the sessions that moved.
func rereadPromptLogs(removed map[string]bool, changed, files, old map[string]FileState, sessions map[string]SessionMeta) {
	if len(removed) == 0 && !promptLogTakeover(changed, old, sessions) {
		return
	}
	for p, f := range files {
		if _, again := changed[p]; again || !isCodexHistory(p) {
			continue
		}
		if _, known := old[p]; known {
			changed[p] = f
		}
	}
}

// widenLogTail adds to ids the sessions whose place in the log's parse moved
// with no new line: the parser skips a session whose transcript is on disk, so
// a rollout or transcript appearing or going takes one out of the parse or
// puts one back. A session the index holds from p that the parse no longer
// returns has its records dropped, and one it returns that the index does not
// hold from p is read in, as reading the log whole did. ss is the whole
// parse.
func widenLogTail(ids map[string]bool, p string, ss []model.Session, held map[string]SessionMeta) map[string]bool {
	got := make(map[string]bool, len(ss))
	for _, s := range ss {
		got[s.ID] = true
		if held[s.Harness+":"+s.ID].Path != p {
			ids[s.ID] = true
		}
	}
	for _, meta := range held {
		if meta.Path == p && !got[meta.ID] {
			ids[meta.ID] = true
		}
	}
	return ids
}

// clipsBySession reports whether a file's clip count is all attributed to
// sessions, as every pass since ClippedSessions has written it.
func clipsBySession(e FileIngest) bool {
	n := 0
	for _, c := range e.ClippedSessions {
		n += c
	}
	return n == e.Clipped
}

// sessionsNamed keeps the sessions of ss whose id is in ids.
func sessionsNamed(ss []model.Session, ids map[string]bool) []model.Session {
	out := ss[:0]
	for _, s := range ss {
		if ids[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// keptFromLogTail reports whether a record of a prompt log read from its
// watermark belongs to a session no new line names, and so is carried.
func keptFromLogTail(r Record, tails map[string]map[string]bool) bool {
	ids, ok := tails[r.SourcePath]
	if !ok {
		return false
	}
	_, id, _ := strings.Cut(r.Key, ":")
	return !ids[id]
}

// clipsOutsideTail is what a prompt log's clip counts keep through a pass
// that reads only the sessions in ids again: theirs start over, the rest stay.
func clipsOutsideTail(e FileIngest, ids map[string]bool) FileIngest {
	keep := map[string]int{}
	n := 0
	for id, c := range e.ClippedSessions {
		if !ids[id] {
			keep[id] = c
			n += c
		}
	}
	e.Clipped, e.ClippedSessions = n, nil
	if len(keep) > 0 {
		e.ClippedSessions = keep
	}
	return e
}
