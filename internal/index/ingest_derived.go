package index

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
)

// withKeptRow is row with what the pass read off a deleted transcript when it
// was on disk. Its records are not the file: a turn another copy holds was
// filed under one of them, and the log holds them in the order passes wrote
// them, so what is read off them in order (the conclusion, the friction lines
// and questions up to their caps) came back other than the pass had it
// (#5535). Those are taken only from a row this version wrote: a version bump
// that changes how they are read, or what is redacted from the conclusion,
// recomputes them from the records.
func withKeptRow(row, kept SessionMeta, sameVersion bool) SessionMeta {
	row.Counted, row.Words, row.LastMsg, row.Opening = kept.Counted, kept.Words, kept.LastMsg, kept.Opening
	if sameVersion {
		row.Settled, row.RanCommand, row.Asked, row.Hit, row.GaveUp = kept.Settled, kept.RanCommand, kept.Asked, kept.Hit, kept.GaveUp
		row.Touched, row.TouchHits = kept.Touched, kept.TouchHits
		row.Rules, row.Revokes, row.Planted = kept.Rules, kept.Revokes, kept.Planted
	}
	return row
}

// carryAccumulatedFrom copies the watermark and accumulated message-derived
// fields from old onto dst before folding an incremental sync batch.
func carryAccumulatedFrom(dst *SessionMeta, old SessionMeta) {
	dst.Counted, dst.Words, dst.LastMsg, dst.Opening = old.Counted, old.Words, old.LastMsg, old.Opening
	dst.Asked, dst.Hit, dst.GaveUp, dst.NoText = old.Asked, old.Hit, old.GaveUp, old.NoText
	dst.Touched, dst.TouchHits, dst.Settled, dst.RanCommand = old.Touched, old.TouchHits, old.Settled, old.RanCommand
}

// resetAccumulated zeroes the watermark and accumulated message-derived fields
// on dst before folding the first sync batch of a newly imported session.
func resetAccumulated(dst *SessionMeta, noText bool) {
	dst.Counted, dst.Words, dst.LastMsg, dst.Opening = 0, 0, 0, 0
	dst.Asked, dst.Hit, dst.GaveUp = nil, nil, false
	dst.Touched, dst.TouchHits, dst.Settled, dst.RanCommand = nil, nil, "", false
	dst.NoText = noText
}

// textOf is the row fields s's messages give.
func textOf(s model.Session) sessionText {
	title, agentTitle := sessionRowTitle(s)
	// The import fields travel with the session, not with the transcript: a
	// rebuild reloads imported sessions out of the index itself, and rebuilding
	// the row from scratch dropped what only the import knew — the note's state
	// and the id it had on the machine it came from (#1049).
	touched, touchHits := topTouchedCounted(s.Messages)
	// Counted, so an append that arrives in the same pass folds only what this
	// did not already see. Without it a session new to the index was derived
	// from here and merged again below, doubling its Words (#1304).
	last := uint64(0)
	if len(s.Messages) > 0 {
		last = messageFingerprint(s.Messages[len(s.Messages)-1])
	}
	rules, revokes := foldUserRules(nil, nil, s.Typed)
	planted := foldPlanted(nil, s.Planted)
	// A repo entry of kind rejected says "reverted" by design; it is not a
	// session that abandoned something partway.
	gave := gaveUp(s.Messages) && !strings.HasPrefix(s.Project, model.RepoOriginPrefix) && s.From != model.OriginRepo
	return sessionText{title: s.Title, meta: SessionMeta{ID: s.ID, Harness: s.Harness, Project: s.Project, Path: s.Path, Title: title, AgentTitle: agentTitle, Started: s.Started, Updated: s.Updated, Touched: touched, TouchHits: touchHits, Counted: len(s.Messages), LastMsg: last, Asked: askedHashes(s.Messages), Hit: frictionHashes(s.Messages), GaveUp: gave, Words: sessionWords(s.Messages), NoText: !holdsText(s), Settled: sessionSettled(s), RanCommand: ranCommand(s.Messages),
		Kind: s.Kind, Parent: s.Parent, Agent: s.Agent, Opening: SessionOpening(s),
		OrigID: s.OrigID, RemoteID: s.RemoteID, From: s.From, Lifecycle: s.Lifecycle, LifecycleNote: s.LifecycleNote, LifecycleAt: s.LifecycleAt,
		Rules: rules, Revokes: revokes, Planted: planted}}
}

// sessionSettled is what this session concluded, taken from its tail.
//
// The same extraction the point-of-action hook used to do per action, moved to
// where the session is already in hand. In real-world stores, doing it at
// the moment of the action cost 133 ms a call against 21 ms when the hook said
// nothing, because it had to rank candidates and then load whole sessions to
// ask which had run the command — and almost every action asks about a command
// the session has not run before, so nothing was ever warm (#3001, #3605).
//
// The tail and the budget are the hook's own, so the text is the text it would
// have produced. Empty when the session settled nothing, which is the same
// answer the hook gave then.
func sessionSettled(s model.Session) string {
	// Only a session that ran a command can ever be asked: the command table
	// names the session, and nothing else reads this field. Extracting for the
	// rest cost 21s of a 51s rebuild on a 2.0 GB corpus for an answer no caller
	// could reach.
	if !ranCommand(s.Messages) {
		return ""
	}
	return settledFrom(s)
}

func ranCommand(ms []model.Message) bool {
	for _, m := range ms {
		if m.Role == roleCommand {
			return true
		}
	}
	return false
}

// settledFrom is sessionSettled without the command gate.
func settledFrom(s model.Session) string {
	const tail, budget = 150, 200
	if len(s.Messages) > tail {
		cp := s
		cp.Messages = s.Messages[len(s.Messages)-tail:]
		s = cp
	}
	cs := digest.Conclusions(s, budget, 1)
	if len(cs) == 0 {
		return ""
	}
	return strings.TrimSpace(cs[0])
}

// extendDerived folds messages appended to an already-indexed session into the
// fields derived from its text.
//
// The append path used to update only what it could read off the new messages
// directly — timestamps, project, title — and left everything derived frozen at
// whatever the session held when it was first seen. A transcript is written to
// while the work happens, so that is the ordinary case, not an edge one: an
// error hit later in a session never entered Hit and so `deja error` could not
// match it by signature; files touched later never entered Touched, so blame
// did not know about them; a session that gave up after its first pass was
// still ranked as one that had not.
//
// Merged rather than recomputed, because this path only ever holds the new
// messages: the file is read from the last watermark, not from the start.
// extendDerived folds a session's new messages into the fields derived from its
// text. ms is the whole session as the source hands it over; meta.Counted says
// how much of it is already in, so re-delivering a live session — which goose
// does on every pass, and which is how a session new to this file arrives — is
// idempotent rather than additive (#1304).
func extendDerived(meta *SessionMeta, ms []model.Message) {
	tail := newMessages(meta, ms)
	if len(tail) == 0 {
		return
	}
	meta.Counted += len(tail)
	if meta.Opening == 0 {
		meta.Opening = SessionOpening(model.Session{Harness: meta.Harness, Messages: tail})
	}
	if meta.NoText && holdsText(model.Session{Messages: tail}) {
		meta.NoText = false
	}
	meta.LastMsg = messageFingerprint(ms[len(ms)-1])
	meta.Words += sessionWords(tail)
	// Capped like the full build caps: a plain union grows on every append,
	// and the manifest holding it is read on every search.
	meta.Asked = mergeCappedU64(meta.Asked, askedHashes(tail), askedQuestionCap)
	meta.Hit = mergeCappedU64(meta.Hit, frictionHashes(tail), frictionSessionCap)
	// Giving up is a state a session reaches, never one it leaves: the phrases
	// that set it are reversals of work already done.
	if gaveUp(tail) {
		meta.GaveUp = true
	}
	if paths, hits := topTouchedCounted(tail); len(paths) > 0 {
		meta.Touched, meta.TouchHits = mergeTouchedCounted(meta.Touched, meta.TouchHits, paths, hits)
	}
	// The line is picked from the session's last 150 messages, and reading a
	// live session whole again on every append costs more than the line is
	// worth. The old line stands in for the messages before the tail: picked
	// against the tail by the same rules, a concluding line already held
	// outranks a newer one that only reports.
	meta.RanCommand = meta.RanCommand || ranCommand(tail)
	if meta.RanCommand {
		win := tail
		if meta.Settled != "" {
			win = append([]model.Message{{Role: "assistant", Text: meta.Settled}}, tail...)
		}
		if s := settledFrom(model.Session{Harness: meta.Harness, Messages: win}); s != "" {
			meta.Settled = s
		}
	}
}

// newMessages is the part of a delivery the derived fields have not seen. The
// fingerprint is matched from the end: a session that repeats a line verbatim
// would otherwise resume from the first copy and fold the rest twice.
func newMessages(meta *SessionMeta, ms []model.Message) []model.Message {
	if len(ms) == 0 {
		return nil
	}
	if meta.LastMsg == 0 {
		return ms
	}
	for i, m := range slices.Backward(ms) {
		if messageFingerprint(m) == meta.LastMsg {
			return ms[i+1:]
		}
	}
	return ms
}

// messageFingerprint identifies one message well enough to find it again in a
// later delivery of the same session. Role, time and text: two adjacent
// messages with the same text differ by time, and a store that rewrites times
// re-folds a tail, which costs a little accuracy and no correctness.
func messageFingerprint(m model.Message) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(m.Role))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.FormatInt(m.Time.UnixNano(), 10)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(m.Text))
	// Zero means "nothing counted yet", so a message that hashes to it takes
	// the neighbouring value rather than resetting the session's state.
	if sum := h.Sum64(); sum != 0 {
		return sum
	}
	return 1
}

// mergeTouchedCounted adds the tail's touches to the counts already held and
// re-ranks. Position alone was all the old merge had: six paths touched once in
// an early batch filled the list, and the file the session actually worked on
// hardest never appeared (#1333, and its real cause #1304).
// A path with no count behind it — the import path derives Touched from records
// and carries none — counts as one, so it keeps its place without outranking a
// file this session actually worked on repeatedly.
func mergeTouchedCounted(have []string, hits []int, add []string, addHits []int) ([]string, []int) {
	count := map[string]int{}
	for i, p := range have {
		n := 1
		if i < len(hits) {
			n = hits[i]
		}
		count[p] += n
	}
	for i, p := range add {
		n := 1
		if i < len(addHits) {
			n = addHits[i]
		}
		count[p] += n
	}
	return rankTouchedCounted(count)
}
