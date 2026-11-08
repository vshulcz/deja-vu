package index

import (
	"hash/fnv"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	anyKnown := len(heads) > 0 || len(wholes) > 0 || len(wholeForks) > 0 || len(headOpenings) > 0 || len(wholeOpenings) > 0
	for _, meta := range m.Sessions {
		if ids[meta.ID] {
			anyKnown = true
			if meta.Kind == "fork" {
				metaForks = append(metaForks, meta)
			} else {
				parentOf(meta.Kind, meta.Parent)
			}
			if meta.Opening != 0 {
				askers[meta.Opening] = append(askers[meta.Opening], meta)
			}
		} else if meta.Parent != "" && ids[meta.Parent] {
			anyKnown = true
		}
	}
	if !anyKnown {
		return out, nil
	}
	held := heldRecords(dir, m)
	byID := viewsFor(m).metasByID()
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
		return !ok || held(fork, p) || endedBy(dir, m, p, startedAt(dir, m, fork))
	}
	for _, wf := range wholeForks {
		p, ok := parentMeta(wf.harness, wf.parent)
		if !ok || wholeHolds(dir, m, []map[string]bool{wf.turns}, p) || endedBy(dir, m, p, wf.started) {
			out[wf.parent] = true
		}
	}
	for _, f := range metaForks {
		if f.Parent != "" && f.Parent != f.ID && forkHolds(f) {
			out[f.Parent] = true
		}
	}
	own := spawnedFamily(m, ids, out, forkHolds)
	for _, meta := range m.Sessions {
		if own[meta.ID] {
			out[meta.ID] = true
		}
		if meta.Opening == 0 || ids[meta.ID] {
			continue
		}
		if headOpenings[meta.Opening] {
			out[meta.ID] = true
			continue
		}
		if ws := wholeOpenings[meta.Opening]; len(ws) > 0 && wholeHolds(dir, m, ws, meta) {
			out[meta.ID] = true
			continue
		}
		for _, a := range askers[meta.Opening] {
			if held(a, meta) {
				out[meta.ID] = true
				break
			}
		}
	}
	return out, lineageCuts(dir, m, out, metaForks, wholeForks, askers, wholeOpenings, parentMeta)
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
func lineageCuts(dir string, m Manifest, out map[string]bool, metaForks []SessionMeta, wholeForks []wholeFork, askers map[uint64][]SessionMeta, wholeOpenings map[uint64][]map[string]bool, parentMeta func(harness, id string) (SessionMeta, bool)) map[string]time.Time {
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
	shared := sharedRecords(dir, m)
	for _, f := range metaForks {
		if p, ok := parentMeta(f.Harness, f.Parent); ok && !out[p.ID] {
			at := shared(f, p)
			if at.IsZero() {
				// Copied with new times: what the source held when the
				// fork started is what it copied.
				at = startedAt(dir, m, f)
			}
			cutAt(p.ID, at)
		}
	}
	for _, wf := range wholeForks {
		if p, ok := parentMeta(wf.harness, wf.parent); ok && !out[p.ID] {
			at := wholeShared(dir, m, wf.turns, p)
			if at.IsZero() {
				at = wf.started
			}
			cutAt(p.ID, at)
		}
	}
	for _, meta := range m.Sessions {
		if meta.Opening == 0 || out[meta.ID] {
			continue
		}
		for _, a := range askers[meta.Opening] {
			cutAt(meta.ID, shared(a, meta))
		}
		for _, w := range wholeOpenings[meta.Opening] {
			cutAt(meta.ID, wholeShared(dir, m, w, meta))
		}
	}
	return cuts
}

// heldRecords answers whether asker holds other's last record, the same role,
// time and text: other is then a copy the asker carries, and nothing in it is
// news to the asker. Records are read only for sessions that open alike, which
// takes a fork, so a store without forks reads none. A read that fails answers
// no, which keeps the session on the page.
func heldRecords(dir string, m Manifest) func(asker, other SessionMeta) bool {
	path := filepath.Join(dir, "records.bin")
	tables := tablesFromManifest(m)
	sets := map[string]map[string]bool{}
	key := func(r Record) string {
		return r.Role + "\x00" + strconv.FormatInt(r.Time.UnixNano(), 10) + "\x00" + r.Text
	}
	return func(asker, other SessionMeta) bool {
		k := asker.Key()
		set, ok := sets[k]
		if !ok {
			if recs, err := recordsForKey(path, tables, k); err == nil {
				set = make(map[string]bool, len(recs))
				for _, r := range recs {
					set[key(r)] = true
				}
			}
			sets[k] = set
		}
		if len(set) == 0 {
			return false
		}
		recs, err := recordsForKey(path, tables, other.Key())
		if err != nil {
			return false
		}
		// What the harness writes into a source after the fork is not work:
		// a backgrounded session gains "No response requested." and a task
		// notification, and the fork that holds every turn before them got
		// its source back as recall (#4251).
		for _, rec := range slices.Backward(recs) {
			if !afterForkNoise(rec) {
				return set[key(rec)]
			}
		}
		return false
	}
}

// endedBy reports whether src's last turn came no later than at, so a fork
// that started at that moment was made after everything src holds. A fork read with no
// times says nothing either way, and its source is left out as it always was;
// a read that fails answers no.
func endedBy(dir string, m Manifest, src SessionMeta, at time.Time) bool {
	if at.IsZero() {
		return true
	}
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), src.Key())
	if err != nil {
		return false
	}
	for _, rec := range slices.Backward(recs) {
		switch r := rec; {
		case afterForkNoise(r), r.Role == roleEdit, r.Role == roleWrote, r.Role == roleFiles, r.Time.IsZero():
			continue
		}
		return !rec.Time.After(at)
	}
	return false
}

// startedAt is when an indexed session started: its own field, or its first
// timed record when the harness left that out.
func startedAt(dir string, m Manifest, meta SessionMeta) time.Time {
	if !meta.Started.IsZero() {
		return meta.Started
	}
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), meta.Key())
	if err != nil {
		return time.Time{}
	}
	for _, r := range recs {
		if !r.Time.IsZero() {
			return r.Time
		}
	}
	return time.Time{}
}

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

// wholeHolds reports whether any of the whole askers holds other's last record
// that is a turn rather than harness noise. File changes are passed over: an
// opencode session's diff store is filed after its turns at the file's mtime,
// and the store a whole asker is read off carries no diff. A read that fails
// answers no.
func wholeHolds(dir string, m Manifest, wholes []map[string]bool, other SessionMeta) bool {
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), other.Key())
	if err != nil {
		return false
	}
	for _, rec := range slices.Backward(recs) {
		switch r := rec; {
		case afterForkNoise(r), r.Role == roleEdit, r.Role == roleWrote, r.Role == roleFiles:
			continue
		}
		k := turnKey(rec.Role, rec.Time.UnixMilli())
		for _, w := range wholes {
			if w[k] {
				return true
			}
		}
		return false
	}
	return false
}

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
	for _, meta := range m.Sessions {
		if meta.ID == id {
			return true
		}
	}
	return false
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
	children := map[string][]string{}
	parent := map[string]string{}
	forks := map[string]SessionMeta{}
	for _, meta := range m.Sessions {
		if meta.Parent == "" || meta.Parent == meta.ID {
			continue
		}
		if meta.IsSpawned() {
			children[meta.Parent] = append(children[meta.Parent], meta.ID)
			parent[meta.ID] = meta.Parent
		} else if meta.Kind == model.KindFork && forkHolds != nil {
			forks[meta.ID] = meta
		}
	}
	up := func(id string) string {
		if p := parent[id]; p != "" {
			return p
		}
		if f, ok := forks[id]; ok && forkHolds(f) {
			return f.Parent
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
		for _, c := range children[id] {
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
