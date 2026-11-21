package index

import (
	"hash/fnv"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// HasAnySession reports whether the manifest holds any session in ids.
func HasAnySession(dir string, ids ...string) bool {
	if len(ids) == 0 {
		return false
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return false
	}
	byID := viewsFor(m).metasByID()
	for _, id := range ids {
		if id != "" && len(byID[id]) > 0 {
			return true
		}
	}
	return false
}

func lineage(dir string, ids map[string]bool, heads, wholes []model.Session) (map[string]bool, map[string]time.Time) {
	out := make(map[string]bool, len(ids))
	for id := range ids {
		if id != "" {
			out[id] = true
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	// A head is an asker the index has not seen yet, so it is the newer of any
	// session it opens alike with: everything indexed it matches, it copied.
	// An indexed asker is checked against what it holds.
	headOpenings := map[uint64]bool{}
	askers := map[uint64][]SessionMeta{}
	// A spawned agent's parent is its own work. A fork's source is left out
	// only when the fork holds the source's last turn: a source that went on
	// after the fork has work the fork never saw (#5293). A head is checked
	// against nothing, being a fork's first prompts, before its source can
	// have gone on.
	parentOf := func(kind, parent string) {
		if parent != "" && (model.SpawnedKind(kind) || kind == model.KindFork) {
			out[parent] = true
		}
	}
	var metaForks []SessionMeta
	wholeForks, wholeOpenings := unindexedAskers(ids, heads, wholes, headOpenings, parentOf)
	m, err := readManifestCached(dir)
	if err != nil {
		// Nothing indexed to hide, so a named source costs nothing.
		for _, wf := range wholeForks {
			out[wf.parent] = true
		}
		return out, nil
	}
	pureIndexed := len(heads) == 0 && len(wholes) == 0
	var views *metaViews
	var cacheKey string
	if pureIndexed {
		views = viewsFor(m)
		cacheKey = lineageCacheKey(dir, ids)
		if hit, hitOK := views.getLineageCached(cacheKey); hitOK {
			return hit.out, hit.cuts
		}
	} else {
		views = viewsFor(m)
	}
	edges := views.spawnEdges()
	byID := views.metasByID()
	anyKnown := len(heads) > 0 || len(wholes) > 0 || len(wholeForks) > 0 || len(headOpenings) > 0 || len(wholeOpenings) > 0
	for id := range ids {
		if id == "" {
			continue
		}
		for _, metaPtr := range byID[id] {
			meta := *metaPtr
			anyKnown = true
			if meta.Kind == "fork" {
				metaForks = append(metaForks, meta)
			} else {
				parentOf(meta.Kind, meta.Parent)
			}
			if meta.Opening != 0 {
				askers[meta.Opening] = append(askers[meta.Opening], meta)
			}
		}
		if edges.hasChild[id] {
			anyKnown = true
		}
	}
	if !anyKnown {
		if pureIndexed {
			views.putLineageCached(cacheKey, out, nil)
		}
		return out, nil
	}
	lr := newLineageReader(dir, m)
	defer lr.close()
	// parentMeta is the session a fork names, in the fork's own harness.
	parentMeta := func(harness, id string) (SessionMeta, bool) {
		for _, p := range byID[id] {
			if p.Harness == harness {
				return *p, true
			}
		}
		return SessionMeta{}, false
	}
	// A fork holds its source's end when it carries the source's last turn,
	// or when it started after that turn: Hermes and others copy with new
	// times, so a copy made after the source's last turn holds all of it.
	// A source the index does not hold in the fork's harness has nothing to
	// hide.
	forkHolds := func(fork SessionMeta) bool {
		p, ok := parentMeta(fork.Harness, fork.Parent)
		return !ok || lr.held(fork, p) || lr.endedBy(p, lr.startedAt(fork))
	}
	for _, wf := range wholeForks {
		p, ok := parentMeta(wf.harness, wf.parent)
		if !ok || lr.wholeHolds([]map[string]bool{wf.turns}, p) || lr.endedBy(p, wf.started) {
			out[wf.parent] = true
		}
	}
	for _, f := range metaForks {
		if f.Parent != "" && f.Parent != f.ID && forkHolds(f) {
			out[f.Parent] = true
		}
	}
	for id := range spawnedFamilyEdges(edges, ids, out, forkHolds) {
		out[id] = true
	}
	hideSharedOpenings(views.metasByOpening(), lr, ids, out, headOpenings, wholeOpenings, askers)
	cuts := lineageCuts(views, lr, out, metaForks, wholeForks, askers, wholeOpenings, parentMeta)
	if pureIndexed {
		views.putLineageCached(cacheKey, out, cuts)
	}
	return out, cuts
}

func hideSharedOpenings(byOpening map[uint64][]*SessionMeta, lr *lineageReader, ids, out map[string]bool, headOpenings map[uint64]bool, wholeOpenings map[uint64][]map[string]bool, askers map[uint64][]SessionMeta) {
	openings := make(map[uint64]bool, len(headOpenings)+len(wholeOpenings)+len(askers))
	for o := range headOpenings {
		openings[o] = true
	}
	for o := range wholeOpenings {
		openings[o] = true
	}
	for o := range askers {
		openings[o] = true
	}
	for opening := range openings {
		for _, metaPtr := range byOpening[opening] {
			meta := *metaPtr
			if ids[meta.ID] || out[meta.ID] {
				continue
			}
			if headOpenings[opening] {
				out[meta.ID] = true
				continue
			}
			if ws := wholeOpenings[opening]; len(ws) > 0 && lr.wholeHolds(ws, meta) {
				out[meta.ID] = true
				continue
			}
			for _, a := range askers[opening] {
				if lr.held(a, meta) {
					out[meta.ID] = true
					break
				}
			}
		}
	}
}

// wholeFork is a fork read off its store in full: the source it names and the
// turns it holds.
type wholeFork struct {
	harness, parent string
	turns           map[string]bool
	started         time.Time
}

// unindexedAskers reads the askers the index does not hold yet: each head's
// opening goes into headOpenings, and the whole ones come back as forks that
// name a source and as turns by opening. parentOf is called for each asker's
// parent as lineage counts it.
func unindexedAskers(ids map[string]bool, heads, wholes []model.Session, headOpenings map[uint64]bool, parentOf func(kind, parent string)) ([]wholeFork, map[uint64][]map[string]bool) {
	var wholeForks []wholeFork
	for _, h := range heads {
		if ids[h.ID] {
			parentOf(h.Kind, h.Parent)
			if o := SessionOpening(h); o != 0 {
				headOpenings[o] = true
			}
		}
	}
	wholeOpenings := map[uint64][]map[string]bool{}
	for _, w := range wholes {
		if !ids[w.ID] {
			continue
		}
		if w.Kind == "fork" && w.Parent != "" {
			wholeForks = append(wholeForks, wholeFork{w.Harness, w.Parent, wholeTurns(w), firstTimed(w)})
		} else {
			parentOf(w.Kind, w.Parent)
		}
		if o := SessionOpening(w); o != 0 {
			wholeOpenings[o] = append(wholeOpenings[o], wholeTurns(w))
		}
	}
	return wholeForks, wholeOpenings
}

// lineageCuts is where each session left out of out is cut for the askers it
// shares turns with.
func lineageCuts(views *metaViews, lr *lineageReader, out map[string]bool, metaForks []SessionMeta, wholeForks []wholeFork, askers map[uint64][]SessionMeta, wholeOpenings map[uint64][]map[string]bool, parentMeta func(harness, id string) (SessionMeta, bool)) map[string]time.Time {
	// What stays on the page and shares turns with an asker is cut at the
	// last turn they share: a source that went on after the fork, a sibling
	// fork. The asker holds everything up to there already (#5335).
	cuts := map[string]time.Time{}
	cutAt := func(id string, at time.Time) {
		if at.IsZero() || out[id] {
			return
		}
		if at.After(cuts[id]) {
			cuts[id] = at
		}
	}
	for _, f := range metaForks {
		if p, ok := parentMeta(f.Harness, f.Parent); ok && !out[p.ID] {
			at := lr.shared(f, p)
			if at.IsZero() {
				// Copied with new times: what the source held when the
				// fork started is what it copied.
				at = lr.startedAt(f)
			}
			cutAt(p.ID, at)
		}
	}
	for _, wf := range wholeForks {
		if p, ok := parentMeta(wf.harness, wf.parent); ok && !out[p.ID] {
			at := lr.wholeShared(wf.turns, p)
			if at.IsZero() {
				at = wf.started
			}
			cutAt(p.ID, at)
		}
	}
	byOpening := views.metasByOpening()
	for opening, as := range askers {
		for _, metaPtr := range byOpening[opening] {
			meta := *metaPtr
			if out[meta.ID] {
				continue
			}
			for _, a := range as {
				cutAt(meta.ID, lr.shared(a, meta))
			}
		}
	}
	for opening, ws := range wholeOpenings {
		for _, metaPtr := range byOpening[opening] {
			meta := *metaPtr
			if out[meta.ID] {
				continue
			}
			for _, w := range ws {
				cutAt(meta.ID, lr.wholeShared(w, meta))
			}
		}
	}
	return cuts
}

// lineageBodyDecodes counts full record-body decodes during lineage checks.
var lineageBodyDecodes atomic.Int64

// firstTimed is the time of a session's first timed message.
func firstTimed(s model.Session) time.Time {
	for _, msg := range s.Messages {
		if !msg.Time.IsZero() {
			return msg.Time
		}
	}
	return time.Time{}
}

// wholeTurns is the role and millisecond of every message a session holds.
func wholeTurns(s model.Session) map[string]bool {
	set := make(map[string]bool, len(s.Messages))
	for _, msg := range s.Messages {
		if !msg.Time.IsZero() {
			set[turnKey(msg.Role, msg.Time.UnixMilli())] = true
		}
	}
	return set
}

func turnKey(role string, ms int64) string { return role + "\x00" + strconv.FormatInt(ms, 10) }

// afterForkNoise reports a record the harness wrote on its own rather than a
// turn anyone took: an envelope under the user role, Claude Code's reply to a
// turn that wanted none, an empty tool output.
func afterForkNoise(r Record) bool {
	t := strings.TrimSpace(r.Text)
	switch {
	case t == "":
		return true
	case r.Role == roleSummary:
		return true
	case r.Role == "user":
		return harnessPreamble(t)
	case r.Role == "assistant":
		return t == "No response requested."
	}
	return false
}

// HasSession reports whether the index holds a session with this id.
func HasSession(dir, id string) bool {
	if id == "" {
		return false
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return false
	}
	return len(viewsFor(m).metasByID()[id]) > 0
}

// spawnedFamily is every sub-agent below the askers and every parent above
// them, however deep. dsh delegates up to three levels and a grandchild names
// the child that spawned it, not the session the person is in, so one step
// each way left the grandchildren on the root's page and the root on theirs.
// It also adds the ancestors to out.
//
// The walk up starts from every session already in out, so a parent only an
// unindexed asker names leads on to its own parent, and it goes on through a
// fork to its source when forkHolds says the fork holds the source's end: a
// sub-agent of a fork counts what the fork counts (#5305).
func spawnedFamily(m Manifest, ids, out map[string]bool, forkHolds func(SessionMeta) bool) map[string]bool {
	return spawnedFamilyEdges(viewsFor(m).spawnEdges(), ids, out, forkHolds)
}

func spawnedFamilyEdges(edges spawnEdges, ids, out map[string]bool, forkHolds func(SessionMeta) bool) map[string]bool {
	up := func(id string) string {
		if p := edges.parent[id]; p != "" {
			return p
		}
		if forkHolds != nil {
			if f, ok := edges.forks[id]; ok && forkHolds(f) {
				return f.Parent
			}
		}
		return ""
	}
	var starts []string
	for id := range ids {
		starts = append(starts, id)
	}
	for id := range out {
		if !ids[id] {
			starts = append(starts, id)
		}
	}
	for _, id := range starts {
		seen := map[string]bool{id: true}
		for p := up(id); p != "" && !seen[p]; p = up(p) {
			seen[p] = true
			out[p] = true
		}
	}
	own := map[string]bool{}
	var queue []string
	for id := range ids {
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, c := range edges.children[id] {
			if !own[c] && !ids[c] {
				own[c] = true
				queue = append(queue, c)
			}
		}
	}
	return own
}

// openingTextBytes is how much of the opening turn the fingerprint reads: the
// index caps a message's text and a transcript read straight off disk does not.
const openingTextBytes = 256

// SessionOpening fingerprints the turn a session opens with: its harness, the
// first user turn's time to the millisecond, and the start of its text after
// redaction, which the index applies before it stores anything. Zero when the
// session has no timed user turn, so a store without times never matches.
func SessionOpening(s model.Session) uint64 {
	for _, msg := range s.Messages {
		// A compacted session opens on the harness's summary, filed under its
		// own role; a fork of it opens on the same record.
		if (msg.Role != "user" && msg.Role != roleSummary) || msg.Time.IsZero() {
			continue
		}
		// The text as the index keeps it, whichever side this runs on: the
		// manifest fingerprints stored messages, a hook a transcript read off
		// disk, and a compacted session's first turn opens on the harness's
		// preamble, which the index strips.
		text, _ := redact.Text(nfcfold.Compose(strings.TrimSpace(stripSelfRecall(msg.Text))))
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if len(text) > openingTextBytes {
			text = text[:openingTextBytes]
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(s.Harness))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strconv.FormatInt(msg.Time.UnixMilli(), 10)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(text))
		if sum := h.Sum64(); sum != 0 {
			return sum
		}
		return 1
	}
	return 0
}
