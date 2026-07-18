package embed

import (
	"context"
	"sort"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// semanticFloor avoids turning unrelated vectors into search results.
const semanticFloor = 0.55

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
	type match struct {
		session model.Session
		record  index.Record
		score   float64
	}
	best := make(map[string]match)
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
			continue
		}
		if old, ok := best[vector.Key]; !ok || score > old.score {
			best[vector.Key] = match{session: session, record: record, score: score}
		}
	}
	out := make([]search.Hit, 0, len(best))
	for _, found := range best {
		out = append(out, search.Hit{
			Session:  found.session,
			Count:    0,
			Snippets: []string{search.Snippet(found.record.Text, o.Query)},
			Score:    found.score,
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
	if len(out) > 10 {
		out = out[:10]
	}
	return out, nil
}
