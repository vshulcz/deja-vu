package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	lifecycleRejected   = model.LifecycleRejected
	lifecycleSuperseded = model.LifecycleSuperseded
	lifecycleStale      = model.LifecycleStale
)

// promotedNoteID returns the id a promoted note has on the machine that made
// it: after a sync the local id is imported-<hash>, and every rule written for
// notes keys on the deja-note- shape (#975).
func promotedNoteID(s model.Session) string {
	return s.PromotedNoteID()
}

// importedLifecycles maps a source session key ("claude:src1") to the state of
// a promoted note about it that arrived by sync. The note travels and keeps
// its original id; the notes.jsonl holding the states does not travel at all,
// so on the receiving machine the index is the only place the link survives.
func importedLifecycles(dir string) map[string]sources.Lifecycle {
	metas, err := index.AllMetaShared(dir)
	if err != nil {
		return nil
	}
	pol := policy.Load()
	out := map[string]sources.Lifecycle{}
	for _, m := range metas {
		if m.Lifecycle == "" || !metaVisible(pol, "", m) {
			continue
		}
		srcKey, ok := m.PromotedNoteSourceKey()
		if !ok {
			continue
		}
		at, _ := time.Parse("2006-01-02", m.LifecycleAt)
		if prev, exists := out[srcKey]; exists && prev.At.After(at) {
			continue
		}
		out[srcKey] = sources.Lifecycle{
			State: m.Lifecycle, Note: m.LifecycleNote, At: at,
		}
	}
	return out
}

// lifecycleResolver resolves the effective decision lifecycle for sessions by
// combining local/repo promoted notes, the session's own indexed lifecycle
// (#975), and synced promoted notes in the index (#1051).
type lifecycleResolver struct {
	dir           string
	states        map[string]sources.Lifecycle
	imported      map[string]sources.Lifecycle
	importedReady bool
}

func newLifecycleResolver(dir string) *lifecycleResolver {
	return &lifecycleResolver{
		dir:    dir,
		states: sources.PromotedLifecycles(),
	}
}

func (r *lifecycleResolver) resolve(s model.Session) (state, note, at string, isNote, ok bool) {
	key := s.Key()
	if src, okSrc := s.PromotedNoteSourceKey(); okSrc {
		isNote = true
		key = src
	}
	lc, found := r.states[key]
	if !found && s.OrigID != "" {
		lc, found = r.states[s.OrigKey()]
	}
	if !found || lc.State == "" {
		if s.Lifecycle != "" {
			return s.Lifecycle, s.LifecycleNote, s.LifecycleAt, isNote, true
		}
		if !r.importedReady && r.dir != "" {
			r.imported, r.importedReady = importedLifecycles(r.dir), true
		}
		lc, found = r.imported[key]
		if !found && s.OrigID != "" {
			lc, found = r.imported[s.OrigKey()]
		}
		if !found || lc.State == "" {
			return "", "", "", isNote, false
		}
	}
	if !lc.At.IsZero() {
		at = lc.At.Format("2006-01-02")
	}
	return lc.State, lc.Note, at, isNote, true
}

// attachLifecycles marks hits whose decision was later rejected, superseded or
// left to go stale.
//
// deja recorded this already: `deja promote <id> --state rejected` writes the
// state and the correction, and prints that the note "now outranks the raw
// transcript in recall". Measured, it did not — the correction only surfaced
// when its own wording matched the query, so asking about a reverted decision
// returned the transcript that made it, reading like current truth. The state
// belongs to the session; ranking was never going to carry it.
func attachLifecycles(dir string, hits []search.Hit) {
	if len(hits) == 0 {
		return
	}
	// An empty local table is not "nothing to attach": a note that arrived by
	// sync carries its state in the index, because the states themselves live
	// in the other machine's notes.jsonl and never travel (#975).
	r := newLifecycleResolver(dir)
	for i := range hits {
		h := &hits[i]
		state, note, at, _, ok := r.resolve(h.Session)
		if !ok || state == model.LifecycleAccepted {
			continue
		}
		h.Lifecycle, h.LifecycleNote, h.LifecycleAt = state, note, at
	}
}

// lifecycleLine is what a reader — human or model — is told about a hit whose
// decision did not hold. The wording says what happened rather than naming the
// state, because "superseded" means nothing to someone who has not read our
// docs, and the model has to act on it without asking.
func lifecycleLine(h search.Hit) string {
	if h.Lifecycle == "" {
		return ""
	}
	var b strings.Builder
	switch h.Lifecycle {
	case "rejected":
		b.WriteString("[this was tried and rejected")
	case "superseded":
		b.WriteString("[a later decision replaced this")
	case "stale":
		b.WriteString("[marked stale — may no longer hold")
	default:
		b.WriteString("[" + recallListingLine(h.Lifecycle))
	}
	if h.LifecycleAt != "" {
		fmt.Fprintf(&b, ", %s", h.LifecycleAt)
	}
	b.WriteString("]")
	if h.LifecycleNote != "" {
		// The note is free text the writer chose, and it travels between
		// machines with the session. On its own line above the snippets, a
		// note spanning several lines prints a "2. [claude] …" header and a
		// "- …" snippet that no session in the store produced — the forgery
		// #1080 fixed for imported project names, here on the note channel.
		b.WriteString(" " + search.SafeNote(recallListingLine(h.LifecycleNote)))
	}
	return b.String()
}

// demoteRejected moves the attempts a person marked rejected below the ones
// they did not, and drops the "earlier attempt" label from a hit whose newer
// rival is one of them.
//
// rejected is the strongest statement someone can make about a piece of their
// own history, and it used to move nothing: the wrong answer stayed first
// while the decision that replaced it was labelled as superseded *by* the
// rejected one, so an agent reading top-down got the wrong answer with a
// recommendation attached (#684).
//
// Only rejected demotes. superseded and stale say "this was true once", which
// is still the best record of how the current answer was reached; rejected
// says "this did not work".
func demoteRejected(hits []search.Hit) int {
	var rejected []search.Hit
	for _, h := range hits {
		if h.Lifecycle == lifecycleRejected {
			rejected = append(rejected, h)
		}
	}
	if len(rejected) == 0 {
		return 0
	}
	for i := range hits {
		h := &hits[i]
		if h.Superseded == "" || h.Lifecycle == lifecycleRejected {
			continue
		}
		// The label carries a date, not an identity, so the rival is the
		// rejected hit in the same project stamped that day.
		for _, r := range rejected {
			if r.Session.Project == h.Session.Project && r.Session.Updated.Local().Format("2006-01-02") == h.Superseded {
				h.Superseded = ""
				break
			}
		}
	}
	before := make([]string, len(hits))
	for i, h := range hits {
		before[i] = h.Session.Key()
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].Lifecycle != lifecycleRejected && hits[j].Lifecycle == lifecycleRejected
	})
	moved := 0
	for i, h := range hits {
		if before[i] != h.Session.Key() {
			moved++
		}
	}
	return moved
}

// demotedNote is the line that says the order was changed, and by whom.
//
// Read top-down, an older session above a newer one with no explanation is
// what a broken ranking looks like; the reason sat four lines further down,
// attached to the result that moved (#694). deja narrates the other times it
// changes what it returns — word forms, ignored terms, the trust policy — and
// a reordering the reader themselves caused is the same kind of fact.
func demotedNote(hits []search.Hit, moved int) string {
	if moved == 0 {
		return ""
	}
	// One decision, not one row: a rejected note and the transcript it was
	// promoted from are both demoted, and counting rows told the reader they
	// had marked two sessions when they marked one (#997).
	notes := promotedNoteSources()
	seen := map[string]bool{}
	n, elsewhere := 0, 0
	for _, h := range hits {
		if h.Lifecycle != lifecycleRejected {
			continue
		}
		key := h.Session.Key()
		if src, ok := notes[h.Session.ID]; ok {
			key = src
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		n++
		// "you marked" is wrong for a decision another machine took back: the
		// state travelled with the note, the judgement was not this reader's
		// (#975).
		if h.Session.IsImported() {
			elsewhere++
		}
	}
	// Reordering the whole result set and then paging it can leave a page with
	// no rejected hit on it; "0 sessions you marked rejected" is not a fact
	// worth spending a line on.
	if n == 0 {
		return ""
	}
	if elsewhere == n {
		return fmt.Sprintf("%d session%s marked rejected on the machine %s came from %s below the rest", n, pluralS(n), theyOrIt(n), verbWere2(n))
	}
	return fmt.Sprintf("%d session%s you marked rejected %s below the rest", n, pluralS(n), verbWere2(n))
}

func verbWere2(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// theyOrIt keeps the sentence about other machines readable for one and many.
func theyOrIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "they"
}

// attachBlameLifecycles is attachLifecycles for blame, which carries its own
// hit type: blame answers "who decided this", and it answered with the
// accepted line of a decision that had been taken back (#1017).
func attachBlameLifecycles(dir string, hits []search.BlameHit, target search.BlameTarget) {
	if len(hits) == 0 {
		return
	}
	r := newLifecycleResolver(dir)
	for i := range hits {
		h := &hits[i]
		state, note, at, isNote, ok := r.resolve(h.Session)
		if !ok {
			continue
		}
		// Accepted too, since #2514. This started as a warning about a decision
		// that had been taken back, and the result was a tool that spoke up
		// about a decision no longer standing and stayed silent about the one
		// that does — on a file whose whole history is that decision, the row
		// carrying the answer read as the oldest and least relevant.
		//
		// Scope an accepted note on a raw session to the file being blamed: a
		// marathon session that touched a hundred files and promoted one note
		// about the repository description otherwise claims that note as the
		// standing decision of every file it touched (#3451).
		if state == model.LifecycleAccepted && !isNote && note != "" && target.FullPath != "" && !mentionsFile(target.FullPath, note) {
			continue
		}
		h.Lifecycle, h.LifecycleNote, h.LifecycleAt = state, note, at
	}
}
