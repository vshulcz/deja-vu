package embed

import (
	"context"
	"sort"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Rerank reorders the top hits by an even blend of their lexical score and
// their cosine similarity to the query.
func Rerank(ctx context.Context, hits []search.Hit, query string, sidecar Sidecar, client *Client) ([]search.Hit, error) {
	if len(hits) == 0 || len(sidecar.Vectors) == 0 {
		return hits, nil
	}
	q, err := client.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	limit := min(len(hits), 64)
	hi := hits[0].Score
	lo := hits[0].Score
	for _, h := range hits[:limit] {
		if h.Score > hi {
			hi = h.Score
		}
		if h.Score < lo {
			lo = h.Score
		}
	}
	// Only candidate sessions need a similarity; skipping the rest keeps the
	// pass proportional to the result page, not the corpus.
	wanted := make(map[string]bool, limit)
	for _, h := range hits[:limit] {
		wanted[h.Session.Harness+":"+h.Session.ID] = true
	}
	cosines := make(map[string]float64)
	seen := make(map[string]bool)
	for _, v := range sidecar.Vectors {
		if !wanted[v.Key] {
			continue
		}
		c := Cosine(q, v.Values)
		if !seen[v.Key] || c > cosines[v.Key] {
			cosines[v.Key], seen[v.Key] = c, true
		}
	}
	for i := range hits[:limit] {
		key := hits[i].Session.Harness + ":" + hits[i].Session.ID
		bm := 1.0
		if hi != lo {
			bm = (hits[i].Score - lo) / (hi - lo)
		}
		hits[i].Score = 0.5*bm + 0.5*cosines[key]
	}
	sort.SliceStable(hits[:limit], func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits, nil
}

var errBadQueryVector = &queryVectorError{}

type queryVectorError struct{}

func (*queryVectorError) Error() string { return "embedding endpoint returned no query vector" }
