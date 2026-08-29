package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	lifecycleRejected   = "rejected"
	lifecycleSuperseded = "superseded"
	lifecycleStale      = "stale"
)

// promotedNoteID returns the id a promoted note has on the machine that made
// it: after a sync the local id is imported-<hash>, and every rule written for
// notes keys on the deja-note- shape (#975).
func promotedNoteID(s model.Session) string {
	if s.Harness != "deja" {
		return ""
	}
	if strings.HasPrefix(s.ID, "deja-note-") {
		return s.ID
	}
	if strings.HasPrefix(s.OrigID, "deja-note-") {
		return s.OrigID
	}
	return ""
}

// importedLifecycles maps a source session key ("claude:src1") to the state of
// a promoted note about it that arrived by sync. The note travels and keeps
// its original id; the notes.jsonl holding the states does not travel at all,
// so on the receiving machine the index is the only place the link survives.
func importedLifecycles(dir string) map[string]sources.Lifecycle {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil
	}
	out := map[string]sources.Lifecycle{}
	for _, m := range metas {
		if m.Harness != "deja" || m.Lifecycle == "" {
			continue
		}
		src, ok := strings.CutPrefix(m.OrigID, "deja-note-")
		if !ok {
			continue
		}
		at, _ := time.Parse("2006-01-02", m.LifecycleAt)
		out[strings.Replace(src, "-", ":", 1)] = sources.Lifecycle{
			State: m.Lifecycle, Note: m.LifecycleNote, At: at,
		}
	}
	return out
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
	states := sources.PromotedLifecycles()
	var imported map[string]sources.Lifecycle
	importedReady := false
	for i := range hits {
		h := &hits[i]
		key := h.Session.Harness + ":" + h.Session.ID
		// A promoted note is filed under its own id, so the state — which is a
		// property of the decision, not of the transcript — never reached it.
		// Recall shows the lines that matched, and a rejection whose words are
		// not in the query simply did not appear: the agent was handed the
		// accepted line of a decision that had been taken back (#974).
		if noteID := promotedNoteID(h.Session); noteID != "" {
			if src, ok := strings.CutPrefix(noteID, "deja-note-"); ok {
				key = strings.Replace(src, "-", ":", 1)
			}
		}
		lc, ok := states[key]
		if !ok || lc.State == "" {
			// An imported note brought its state with it: the machine that
			// made the decision keeps the states, and notes.jsonl does not
			// travel (#975).
			if h.Session.Lifecycle != "" && h.Session.Lifecycle != "accepted" {
				h.Lifecycle, h.LifecycleNote, h.LifecycleAt = h.Session.Lifecycle, h.Session.LifecycleNote, h.Session.LifecycleAt
				continue
			}
			// The rejection and the transcript it rejects can both arrive by
			// sync. Each keeps its original id, so the note still names the
			// session it is about — but only the index remembers, and the
			// transcript came back with no mark on it: the query a person
			// actually types returned a reverted decision as current (#1051).
			if !importedReady {
				imported, importedReady = importedLifecycles(dir), true
			}
			lc, ok = imported[key]
			if !ok && h.Session.OrigID != "" {
				lc, ok = imported[h.Session.Harness+":"+h.Session.OrigID]
			}
			if !ok || lc.State == "" || lc.State == "accepted" {
				continue
			}
		} else if lc.State == "accepted" {
			continue
		}
		h.Lifecycle = lc.State
		h.LifecycleNote = lc.Note
		if !lc.At.IsZero() {
			h.LifecycleAt = lc.At.Format("2006-01-02")
		}
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
			if r.Session.Project == h.Session.Project && r.Session.Updated.Format("2006-01-02") == h.Superseded {
				h.Superseded = ""
				break
			}
		}
	}
	before := make([]string, len(hits))
	for i, h := range hits {
		before[i] = h.Session.Harness + ":" + h.Session.ID
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].Lifecycle != lifecycleRejected && hits[j].Lifecycle == lifecycleRejected
	})
	moved := 0
	for i, h := range hits {
		if before[i] != h.Session.Harness+":"+h.Session.ID {
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
		key := h.Session.Harness + ":" + h.Session.ID
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
		if strings.HasPrefix(h.Session.Project, "imported:") {
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
func attachBlameLifecycles(hits []search.BlameHit) {
	if len(hits) == 0 {
		return
	}
	states := sources.PromotedLifecycles()
	for i := range hits {
		h := &hits[i]
		key := h.Session.Harness + ":" + h.Session.ID
		if noteID := promotedNoteID(h.Session); noteID != "" {
			if src, ok := strings.CutPrefix(noteID, "deja-note-"); ok {
				key = strings.Replace(src, "-", ":", 1)
			}
		}
		lc, ok := states[key]
		if !ok || lc.State == "" {
			if h.Session.Lifecycle != "" {
				h.Lifecycle, h.LifecycleNote, h.LifecycleAt = h.Session.Lifecycle, h.Session.LifecycleNote, h.Session.LifecycleAt
			}
			continue
		}
		// Accepted too, since #2514. This started as a warning about a decision
		// that had been taken back, and the result was a tool that spoke up
		// about a decision no longer standing and stayed silent about the one
		// that does — on a file whose whole history is that decision, the row
		// carrying the answer read as the oldest and least relevant.
		h.Lifecycle, h.LifecycleNote = lc.State, lc.Note
		if !lc.At.IsZero() {
			h.LifecycleAt = lc.At.Format("2006-01-02")
		}
	}
}
