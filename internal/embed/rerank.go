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
	// A candidate whose session has no vectors yet — indexed since the last
	// `deja embed`, or synced from another machine — is not known to be
	// unrelated. Scoring it as cosine 0 sank the newest sessions below older
	// ones they outranked on keywords; the candidates' mean similarity leaves
	// its keyword score to decide.
	neutral := 0.0
	for _, c := range cosines {
		neutral += c
	}
	if len(cosines) > 0 {
		neutral /= float64(len(cosines))
	}
	for i := range hits[:limit] {
		key := hits[i].Session.Harness + ":" + hits[i].Session.ID
		bm := 1.0
		if hi != lo {
			bm = (hits[i].Score - lo) / (hi - lo)
		}
		cos, ok := cosines[key]
		if !ok {
			cos = neutral
		}
		hits[i].Score = 0.5*bm + 0.5*cos
	}
	sort.SliceStable(hits[:limit], func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits, nil
}

var errBadQueryVector = &queryVectorError{}

type queryVectorError struct{}

func (*queryVectorError) Error() string { return "embedding endpoint returned no query vector" }
