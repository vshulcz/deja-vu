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

// semanticFloor avoids turning unrelated vectors into search results.
const semanticFloor = 0.55

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
	query, err := client.Embed(ctx, []string{o.Query})
	if err != nil {
		return nil, err
	}
	if len(query) != 1 {
		return nil, errBadQueryVector
	}
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
		byKey[s.Harness+":"+s.ID] = s
	}
	byOffset := make(map[int64]index.Record, len(records))
	for _, record := range records {
		byOffset[record.Offset] = record.Record
	}
	best := make(map[string]match)
	heldNotes := make(map[string]match)
	for _, vector := range sidecar.Vectors {
		session, ok := byKey[vector.Key]
		if !ok {
			continue
		}
		record, ok := byOffset[vector.Offset]
		if !ok || (o.Role != "" && record.Role != o.Role) {
			continue
		}
		score := Cosine(query[0], vector.Values)
		if score < semanticFloor {
			// A promoted note is held rather than dropped. It is one distilled
			// line where its transcript is a whole session, so it is often
			// worded further from the query than the source it was made from —
			// and then the floor cut the note while serving the source, which
			// is the answer `promote` says it just put in front (#2803). It is
			// only ever served beside that source, below.
			if session.Harness == notesHarness && strings.HasPrefix(session.ID, notePrefix) {
				if old, ok := heldNotes[vector.Key]; !ok || score > old.score {
					heldNotes[vector.Key] = match{session: session, record: record, score: score}
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

// notePrefix is how a promoted note's id begins; sources.PromotedNoteID builds
// the rest of it from the source session's key.
const notePrefix = "deja-note-"

// notesHarness is the pseudo-harness deja files its own notes under.
const notesHarness = "deja"

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
		found, ok := held[notesHarness+":"+notePrefix+hit.Session.Harness+"-"+hit.Session.ID]
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
	return out
}
