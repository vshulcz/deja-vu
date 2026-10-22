// Command searchbench times search on a real index, in process, so the number
// is the search rather than the process start.
//
// The front page claims ~12 ms over 3.3 GB and nobody had measured it since the
// claim was written. A claim we make publicly and never check is a claim that
// drifts.
//
//	DEJA_INDEX_DIR=~/.cache/deja/index.db go run ./scripts/searchbench
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
)

func main() {
	runs := flag.Int("runs", 12, "timed runs per query")
	flag.Parse()

	dir := index.DefaultDir()
	if err := index.EnsureForSearch(dir, query.Options{Query: "warm", All: true}, false, io.Discard); err != nil {
		fmt.Fprintln(os.Stderr, "ensure:", err)
		os.Exit(1)
	}

	// A spread of shapes: one rare token, a common one, a phrase, a
	// multi-term AND, and something that will miss and fall down the ladder.
	queries := []string{
		"pgbouncer",
		"connection pool exhausted",
		"index",
		"prepared statements driver upgrade",
		"zzzznothingmatchesthis",
		"what did we decide about the queue",
	}

	fmt.Printf("index: %s\n", dir)
	var all []time.Duration
	for _, q := range queries {
		o := query.Options{Query: q, All: true}
		// Warm the caches so the first run's page faults do not become the
		// headline.
		if _, err := index.Search(dir, o); err != nil {
			fmt.Fprintln(os.Stderr, q, err)
			continue
		}
		var ds []time.Duration
		hits := 0
		for range *runs {
			start := time.Now()
			r, err := index.Search(dir, o)
			ds = append(ds, time.Since(start))
			if err != nil {
				fmt.Fprintln(os.Stderr, q, err)
				break
			}
			hits = len(r)
		}
		slices.Sort(ds)
		all = append(all, ds...)
		fmt.Printf("  %-38q %4d hits · median %7.2f ms · p90 %7.2f ms\n",
			q, hits, ms(ds[len(ds)/2]), ms(ds[len(ds)*9/10]))
	}
	slices.Sort(all)
	fmt.Printf("overall median %.2f ms · p90 %.2f ms · %d samples\n",
		ms(all[len(all)/2]), ms(all[len(all)*9/10]), len(all))
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
