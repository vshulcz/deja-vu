package embed

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// semanticFloor avoids turning unrelated vectors into search results on
// high-baseline models (e.g. nomic-embed-text).
const semanticFloor = 0.55

// lowCosineFloor is the floor for embedding models whose cosine distribution
// separates relevant and unrelated passages around 0.40 rather than 0.55
// (e.g. voyage-4-large, text-embedding-3-* — #5970).
const lowCosineFloor = 0.40

// match is one vector's session, the record it came from and how close it
// scored — the unit both the served set and the held notes are kept in.
type match struct {
	session model.Session
	vector  Vector
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
	if sameLayout(sidecar.Generation, gen) && o.Role == "" {
		if hits, ok, oerr := semanticHitsByOffset(dir, o, sidecar, query, byKey); ok || oerr != nil {
			return hits, oerr
		}
	}
	records, err := index.ReadRecords(dir)
	if err != nil {
		return nil, err
	}
	lookup := newRecordLookup(records, sameLayout(sidecar.Generation, gen), identOf(sidecar.Version))
	out, _, _, _ := rankVectors(o, sidecar, query, byKey, func(v Vector) (index.Record, bool) {
		found, ok := lookup.find(v)
		return found.Record, ok
	})
	return out, nil
}

func semanticHitsByOffset(dir string, o search.Options, sidecar Sidecar, query []float32, byKey map[string]model.Session) ([]search.Hit, bool, error) {
	out, best, heldNotes, floor := rankVectors(o, sidecar, query, byKey, nil)
	offsets := make([]int64, 0, len(out))
	for _, hit := range out {
		offsets = append(offsets, hitMatch(hit, best, heldNotes, floor).vector.Offset)
	}
	records, err := index.ReadRecordsAt(dir, offsets)
	if err != nil {
		return nil, false, err
	}
	lookup := newRecordLookup(records, true, identOf(sidecar.Version))
	for i, hit := range out {
		found, ok := lookup.find(hitMatch(hit, best, heldNotes, floor).vector)
		if !ok {
			return nil, false, nil
		}
		q := o.Query
		if hit.Score < floor {
			q = ""
		}
		out[i].Snippets = []string{search.Snippet(found.Record.Text, q)}
	}
	return out, true, nil
}

func hitMatch(hit search.Hit, best, heldNotes map[string]match, floor float64) match {
	if hit.Score < floor {
		return heldNotes[model.SessionKey(notesHarness, hit.Session.PromotedNoteID())]
	}
	return best[hit.Session.Key()]
}

func effectiveSemanticFloor(modelName string, candidates map[string]match) float64 {
	low := strings.ToLower(modelName)
	for _, family := range []string{"voyage", "text-embedding-3", "embed-english", "embed-multilingual", "embed-v", "jina-embeddings"} {
		if strings.Contains(low, family) {
			return lowCosineFloor
		}
	}
	if len(candidates) >= 8 {
		scores := make([]float64, 0, len(candidates))
		for _, c := range candidates {
			scores = append(scores, c.score)
		}
		sort.Float64s(scores)
		if p50 := scores[len(scores)/2]; p50 < 0.35 {
			return max(lowCosineFloor, p50+0.20)
		}
	}
	return semanticFloor
}

func rankVectors(o search.Options, sidecar Sidecar, query []float32, byKey map[string]model.Session, find func(Vector) (index.Record, bool)) ([]search.Hit, map[string]match, map[string]match, float64) {
	candidates := make(map[string]match)
	for _, vector := range sidecar.Vectors {
		session, ok := byKey[vector.Key]
		if !ok {
			continue
		}
		var record index.Record
		if find != nil {
			record, ok = find(vector)
			if !ok || (o.Role != "" && !model.RoleMatches(record.Role, o.Role)) {
				continue
			}
		}
		score := Cosine(query, vector.Values)
		if old, ok := candidates[vector.Key]; !ok || score > old.score {
			candidates[vector.Key] = match{session: session, vector: vector, record: record, score: score}
		}
	}
	floor := effectiveSemanticFloor(sidecar.Model, candidates)
	best := make(map[string]match)
	heldNotes := make(map[string]match)
	for key, m := range candidates {
		if m.score < floor {
			// A promoted note is held rather than dropped. It is one distilled
			// line where its transcript is a whole session, so it is often
			// worded further from the query than the source it was made from —
			// and then the floor cut the note while serving the source, which
			// is the answer `promote` says it just put in front (#2803). It is
			// only ever served beside that source, below.
			if noteID := m.session.PromotedNoteID(); noteID != "" {
				nkey := model.SessionKey(notesHarness, noteID)
				if old, ok := heldNotes[nkey]; !ok || m.score > old.score {
					heldNotes[nkey] = m
				}
			}
			continue
		}
		best[key] = m
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
	return out, best, heldNotes, floor
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
