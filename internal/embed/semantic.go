package embed

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// defaultSemanticFloor avoids turning unrelated vectors into search results.
const defaultSemanticFloor = 0.55

// semanticFloor is the cosine a vector has to reach to be served. Models do not
// share a cosine scale, so DEJA_EMBED_FLOOR overrides the default for one whose
// related pairs sit below it. A value that is not a number from -1 to 1 is
// ignored, so a typo cannot open the gate.
func semanticFloor() float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("DEJA_EMBED_FLOOR")), 64)
	if err != nil || !(v >= -1 && v <= 1) { // negated so NaN is refused too
		return defaultSemanticFloor
	}
	return v
}

// match is one vector's session, the record it came from and how close it
// scored — the unit both the served set and the held notes are kept in.
type match struct {
	session model.Session
	record  index.Record
	score   float64
}

// SemanticSearch searches every covered record and returns the best vector
// match per session. The caller is responsible for checking generation and
// endpoint availability before calling it.
func SemanticSearch(ctx context.Context, dir string, o search.Options, sidecar Sidecar, client *Client) ([]search.Hit, error) {
	query, err := client.EmbedQuery(ctx, o.Query)
	if err != nil {
		return nil, err
	}
	return semanticHitsForQuery(dir, o, sidecar, query)
}

func semanticHitsForQuery(dir string, o search.Options, sidecar Sidecar, query []float32) ([]search.Hit, error) {
	if Stale(dir, sidecar) {
		return nil, nil
	}
	records, err := index.ReadRecords(dir)
	if err != nil {
		return nil, err
	}
	metaSessions, err := index.RecentMatching(dir, 0, o)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]model.Session, len(metaSessions))
	for _, s := range metaSessions {
		byKey[s.Key()] = s
	}
	gen, err := index.Generation(dir)
	if err != nil {
		return nil, err
	}
	lookup := newRecordLookup(records, sameLayout(sidecar.Generation, gen), identOf(sidecar.Version))
	floor := semanticFloor()
	best := make(map[string]match)
	heldNotes := make(map[string]match)
	for _, vector := range sidecar.Vectors {
		session, ok := byKey[vector.Key]
		if !ok {
			continue
		}
		found, ok := lookup.find(vector)
		record := found.Record
		if !ok || (o.Role != "" && !model.RoleMatches(record.Role, o.Role)) {
			continue
		}
		score := Cosine(query, vector.Values)
		if score < floor {
			// A promoted note is held rather than dropped. It is one distilled
			// line where its transcript is a whole session, so it is often
			// worded further from the query than the source it was made from —
			// and then the floor cut the note while serving the source, which
			// is the answer `promote` says it just put in front (#2803). It is
			// only ever served beside that source, below.
			if noteID := session.PromotedNoteID(); noteID != "" {
				key := model.SessionKey(notesHarness, noteID)
				if old, ok := heldNotes[key]; !ok || score > old.score {
					heldNotes[key] = match{session: session, record: record, score: score}
				}
			}
			continue
		}
		if old, ok := best[vector.Key]; !ok || score > old.score {
			best[vector.Key] = match{session: session, record: record, score: score}
		}
	}
	out := make([]search.Hit, 0, len(best))
	for _, found := range best {
		out = append(out, search.Hit{
			Session:    found.session,
			Count:      0,
			Snippets:   []string{search.Snippet(found.record.Text, o.Query)},
			Score:      found.score,
			Tier:       search.TierSemantic,
			TierDetail: fmt.Sprintf("%.2f", found.score),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if !out[i].Session.Updated.Equal(out[j].Session.Updated) {
			return out[i].Session.Updated.After(out[j].Session.Updated)
		}
		return out[i].Session.ID < out[j].Session.ID
	})
	out = withHeldNotes(out, heldNotes)
	search.LiftNotesAboveTheirSource(out)
	if len(out) > 10 {
		out = out[:10]
	}
	return out, nil
}

// notesHarness is the pseudo-harness deja files its own notes under.
const notesHarness = model.NotesHarness

// withHeldNotes puts back the notes the floor cut, but only those whose own
// source is being served — a note nobody asked about stays out, and one that
// distils an answer already in the answer travels with it.
//
// Before the lift below, so the pair is ordered by the same rule every other
// tier uses, and before the cut to ten, so a note cannot be the element the
// truncation takes.
func withHeldNotes(out []search.Hit, held map[string]match) []search.Hit {
	if len(held) == 0 {
		return out
	}
	for _, hit := range out {
		if hit.Session.Harness == notesHarness {
			continue
		}
		for _, noteID := range hit.Session.CandidatePromotedNoteIDs() {
			found, ok := held[model.SessionKey(notesHarness, noteID)]
			if !ok {
				continue
			}
			out = append(out, search.Hit{
				Session:    found.session,
				Snippets:   []string{search.Snippet(found.record.Text, "")},
				Score:      found.score,
				Tier:       search.TierSemantic,
				TierDetail: fmt.Sprintf("%.2f", found.score),
			})
		}
	}
	return out
}
