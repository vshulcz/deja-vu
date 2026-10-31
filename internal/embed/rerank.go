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
	return rerankWithQuery(hits, q, sidecar), nil
}

// rrfK is the reciprocal-rank smoothing constant used when fusing lexical and
// vector-first rankings (#4976).
const rrfK = 60

// RerankAndFuse reorders the lexical candidates with Rerank and, when the
// sidecar holds sessions above semanticFloor that the lexical pass missed,
// fuses both legs by reciprocal rank (1/(60+rank)) using a single query
// embedding.
func RerankAndFuse(ctx context.Context, dir string, hits []search.Hit, o search.Options, sidecar Sidecar, client *Client) ([]search.Hit, bool, error) {
	if len(hits) == 0 || len(sidecar.Vectors) == 0 {
		return hits, false, nil
	}
	q, err := client.EmbedQuery(ctx, o.Query)
	if err != nil {
		return nil, false, err
	}
	hits = rerankWithQuery(hits, q, sidecar)
	if dir == "" {
		return hits, false, nil
	}
	semHits, _ := semanticHitsForQuery(dir, o, sidecar, q)
	if len(semHits) == 0 {
		return hits, false, nil
	}
	lexRank := make(map[string]int, len(hits))
	for i, h := range hits {
		lexRank[h.Session.Harness+":"+h.Session.ID] = i + 1
	}
	vecRank := make(map[string]int, len(semHits))
	var extra []search.Hit
	for i, h := range semHits {
		key := h.Session.Harness + ":" + h.Session.ID
		vecRank[key] = i + 1
		if _, ok := lexRank[key]; !ok {
			extra = append(extra, h)
		}
	}
	if len(extra) == 0 {
		return hits, false, nil
	}
	fused := make([]search.Hit, 0, len(hits)+len(extra))
	fused = append(fused, hits...)
	fused = append(fused, extra...)
	for i := range fused {
		key := fused[i].Session.Harness + ":" + fused[i].Session.ID
		score := 0.0
		if r := lexRank[key]; r > 0 {
			score += 1.0 / float64(rrfK+r)
		}
		if r := vecRank[key]; r > 0 {
			score += 1.0 / float64(rrfK+r)
		}
		fused[i].Score = score
	}
	sort.SliceStable(fused, func(i, j int) bool {
		if fused[i].Score != fused[j].Score {
			return fused[i].Score > fused[j].Score
		}
		li := lexRank[fused[i].Session.Harness+":"+fused[i].Session.ID] > 0
		lj := lexRank[fused[j].Session.Harness+":"+fused[j].Session.ID] > 0
		if li != lj {
			return li
		}
		return fused[i].Session.Updated.After(fused[j].Session.Updated)
	})
	return fused, true, nil
}

func rerankWithQuery(hits []search.Hit, q []float32, sidecar Sidecar) []search.Hit {
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
	return hits
}

var errBadQueryVector = &queryVectorError{}

type queryVectorError struct{}

func (*queryVectorError) Error() string { return "embedding endpoint returned no query vector" }
