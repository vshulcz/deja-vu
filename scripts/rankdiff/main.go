// Command rankdiff dumps the top results of a fixed query set against a real
// index, so two builds can be compared line for line.
//
// It exists because the benchmarks in this repo cannot see changes to how much
// of a large session gets read: LongMemEval haystack sessions are a few turns
// each, so any per-session bound is inert there. A ranking change that only
// shows up on real stores needs a real store to show up on.
//
//	DEJA_INDEX_DIR=~/.cache/deja/index.db go run ./scripts/rankdiff > before.txt
//	# change something
//	DEJA_INDEX_DIR=~/.cache/deja/index.db go run ./scripts/rankdiff > after.txt
//	diff before.txt after.txt
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A spread of shapes, weighted towards the ones a per-session bound can move:
// common words that resolve thousands of records out of a handful of long
// sessions.
var queries = []string{
	"index", "error", "pool", "queue", "timeout", "migration",
	"pgbouncer", "connection pool exhausted", "prepared statements",
	"what did we decide about the queue", "why is the build slow",
	"how do we handle retries", "redis eviction", "hydration mismatch",
	"bundle size", "jwt refresh", "clock skew", "trigram",
	"reindex memory", "backup restore", "flaky test", "coverage gate",
	"release checklist", "windows manifest", "mcp server", "recall budget",
}

func main() {
	top := flag.Int("top", 10, "results to print per query")
	flag.Parse()

	dir := index.DefaultDir()
	for _, q := range queries {
		o := query.Options{Query: q, All: true}
		res, err := index.SearchDetailed(dir, o)
		if err != nil {
			fmt.Fprintln(os.Stderr, q, err)
			continue
		}
		// index.Search returns candidates in map order; the ranking the user
		// sees is search.Run on top of it. Comparing the candidate set alone
		// compares nothing but Go's map iteration.
		o.Tier = res.Tier
		var hits []search.Hit
		if res.Tier == search.TierRelevance {
			hits = search.RelevanceHits(res.Sessions, index.RelevanceMatchTerms(q))
		} else {
			ranked, rerr := search.RunDetailed(res.Sessions, o)
			if rerr != nil {
				fmt.Fprintln(os.Stderr, q, rerr)
				continue
			}
			hits = ranked.Hits
		}
		fmt.Printf("== %s [%s] %d sessions\n", q, res.Tier, len(res.Sessions))
		for i, h := range hits {
			if i >= *top {
				break
			}
			fmt.Printf("   %2d %s:%s  %.4f\n", i+1, h.Session.Harness, h.Session.ID, h.Score)
		}
	}
}
