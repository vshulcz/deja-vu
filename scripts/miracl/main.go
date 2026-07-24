// Command miracl measures deja's retrieval on MIRACL (Wikipedia passage
// retrieval with human relevance judgments) in any of its languages, through
// the exact production path: passages are written as real transcript files,
// indexed by the normal pipeline, and queried with the verbatim question.
//
// MIRACL's official task retrieves over the full language corpus (millions of
// passages) and scores nDCG@10. This harness builds a bounded pool instead —
// every dev-set relevant passage plus random same-language passages up to
// -pool — and reports hit@k and per-query recall@k over that pool. Numbers are
// therefore comparable ACROSS LANGUAGES here, not against the MIRACL
// leaderboard.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

type passage struct {
	DocID string `json:"docid"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

func main() {
	lang := flag.String("lang", "ru", "language code (matches the corpus/topics/qrels files)")
	dir := flag.String("data", ".", "directory holding <lang>-*.jsonl.gz, topics-<lang>.tsv, qrels-<lang>.tsv")
	pool := flag.Int("pool", 200000, "passage pool size: all relevant passages plus random fill")
	limit := flag.Int("limit", 0, "run only the first N queries (0 = all)")
	seed := flag.Int64("seed", 20260725, "sampling seed")
	dumpMisses := flag.String("dump-misses", "", "write a JSONL miss report to this path")
	flag.Parse()

	topics, order, err := readTopics(filepath.Join(*dir, "topics-"+*lang+".tsv"))
	if err != nil {
		fatal(err)
	}
	qrels, err := readQrels(filepath.Join(*dir, "qrels-"+*lang+".tsv"))
	if err != nil {
		fatal(err)
	}
	gold := map[string]bool{}
	for _, docs := range qrels {
		for d := range docs {
			gold[d] = true
		}
	}
	fmt.Printf("MIRACL-%s · %d dev queries · %d relevant passages\n", *lang, len(order), len(gold))

	tmp, err := os.MkdirTemp("", "miracl")
	if err != nil {
		fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-work-miracl")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		fatal(err)
	}

	t0 := time.Now()
	written, haveGold, err := writePool(filepath.Join(*dir), *lang, proj, gold, *pool, *seed)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("pool: %d passages written in %s · relevant passages present: %d/%d (%.1f%%)\n",
		written, time.Since(t0).Round(time.Second), len(haveGold), len(gold),
		100*float64(len(haveGold))/float64(len(gold)))

	idx := filepath.Join(tmp, "index.db")
	_ = os.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	_ = os.Setenv("DEJA_INDEX_DIR", idx)
	t1 := time.Now()
	if err := index.Ensure(idx, "claude", true, nil); err != nil {
		fatal(err)
	}
	buildFor := time.Since(t1)
	var indexBytes int64
	_ = filepath.Walk(idx, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			indexBytes += fi.Size()
		}
		return nil
	})
	fmt.Printf("index: built in %s, %.0f MB\n\n", buildFor.Round(time.Second), float64(indexBytes)/1e6)

	var missFile *os.File
	if *dumpMisses != "" {
		if missFile, err = os.Create(*dumpMisses); err != nil {
			fatal(err)
		}
		defer func() { _ = missFile.Close() }()
	}

	tiers := map[string]int{}
	skipped := 0
	var (
		n, h1, h5, h10, h20 int
		mrr                 float64
		recall              = map[int]float64{}
		times               []time.Duration
	)
	for qi, qid := range order {
		if *limit > 0 && qi >= *limit {
			break
		}
		want := map[string]bool{}
		for d := range qrels[qid] {
			if haveGold[d] {
				want[d] = true
			}
		}
		if len(want) == 0 {
			skipped++
			continue
		}
		rank, ranked, tier, elapsed, err := ask(idx, topics[qid], want)
		if err != nil {
			fatal(err)
		}
		tiers[tier]++
		times = append(times, elapsed)
		n++
		if rank >= 1 {
			mrr += 1 / float64(rank)
		}
		for _, k := range []int{1, 5, 10, 20} {
			got := 0
			for i, id := range ranked {
				if i >= k {
					break
				}
				if want[id] {
					got++
				}
			}
			recall[k] += float64(got) / float64(len(want))
		}
		switch {
		case rank == 0:
		case rank <= 1:
			h1++
			h5++
			h10++
			h20++
		case rank <= 5:
			h5++
			h10++
			h20++
		case rank <= 10:
			h10++
			h20++
		case rank <= 20:
			h20++
		}
		if missFile != nil && rank != 1 {
			top := ranked
			if len(top) > 5 {
				top = top[:5]
			}
			rec := map[string]any{"qid": qid, "query": topics[qid], "rank": rank, "gold": keys(want), "top5": top}
			b, _ := json.Marshal(rec)
			_, _ = missFile.Write(append(b, '\n'))
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	fmt.Printf("%-12s %6s %8s %8s %8s %8s %8s\n", "lang", "n", "hit@1", "hit@5", "hit@10", "hit@20", "MRR")
	fmt.Printf("%-12s %6d %7.1f%% %7.1f%% %7.1f%% %7.1f%%   %.3f\n", *lang, n,
		pct(h1, n), pct(h5, n), pct(h10, n), pct(h20, n), mrr/float64(n))
	fmt.Printf("%-12s %6s %7.1f%% %7.1f%% %7.1f%% %7.1f%%\n", "recall@k", "",
		100*recall[1]/float64(n), 100*recall[5]/float64(n), 100*recall[10]/float64(n), 100*recall[20]/float64(n))
	if len(times) > 0 {
		fmt.Printf("median search: %s\n", times[len(times)/2].Round(time.Microsecond))
	}
	tierNames := make([]string, 0, len(tiers))
	for t := range tiers {
		tierNames = append(tierNames, t)
	}
	sort.Strings(tierNames)
	parts := make([]string, 0, len(tierNames))
	for _, t := range tierNames {
		name := t
		if name == "" {
			name = "exact"
		}
		parts = append(parts, fmt.Sprintf("%s %d", name, tiers[t]))
	}
	fmt.Printf("tiers: %s\n", strings.Join(parts, " · "))
	if skipped > 0 {
		fmt.Printf("skipped %d queries whose relevant passages are outside the downloaded shards\n", skipped)
	}
}

// ask runs one query through the production search path and returns the rank
// of the first relevant passage (1-based, 0 if absent from the top 50) plus
// the ranked docids.
func ask(dir, q string, want map[string]bool) (int, []string, string, time.Duration, error) {
	o := query.Options{Query: q, All: true}
	t0 := time.Now()
	result, err := index.SearchWithRecoveryDetailed(dir, o, nil)
	if err != nil {
		return 0, nil, "", 0, err
	}
	o.Tier = result.Tier
	if result.Stemmed {
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
		o.FuzzyVariants = result.Variants
	}
	var hits []search.Hit
	if result.Tier == search.TierRelevance {
		hits = search.RelevanceHits(result.Sessions, index.RelevanceTerms(q))
	} else if hits, err = search.Run(result.Sessions, o); err != nil {
		return 0, nil, "", 0, err
	}
	elapsed := time.Since(t0)
	ranked := make([]string, 0, 50)
	for i, h := range hits {
		if i >= 50 {
			break
		}
		ranked = append(ranked, unsanitize(h.Session.ID))
	}
	for i, id := range ranked {
		if want[id] {
			return i + 1, ranked, string(result.Tier), elapsed, nil
		}
	}
	return 0, ranked, string(result.Tier), elapsed, nil
}

// writePool writes every relevant passage plus a random sample of the rest as
// Claude-format transcripts, one file per passage.
func writePool(dir, lang, proj string, gold map[string]bool, pool int, seed int64) (int, map[string]bool, error) {
	have := map[string]bool{}
	shards, err := filepath.Glob(filepath.Join(dir, lang+"-*.jsonl.gz"))
	if err != nil {
		return 0, have, err
	}
	if len(shards) == 0 {
		return 0, have, fmt.Errorf("no %s-*.jsonl.gz shards in %s", lang, dir)
	}
	sort.Strings(shards)
	rng := rand.New(rand.NewSource(seed))
	// Reservoir-free two-pass-free approach: golds always kept, others kept
	// with a probability that fills the remaining budget on a corpus of
	// unknown size — a light bias toward early shards is acceptable for a
	// distractor pool.
	keepProb := 0.05
	written := 0
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, shard := range shards {
		f, err := os.Open(shard)
		if err != nil {
			return written, have, err
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			_ = f.Close()
			return written, have, err
		}
		sc := bufio.NewScanner(gz)
		sc.Buffer(make([]byte, 1<<20), 1<<22)
		for sc.Scan() {
			var p passage
			if json.Unmarshal(sc.Bytes(), &p) != nil || p.DocID == "" {
				continue
			}
			if !gold[p.DocID] {
				if written >= pool || rng.Float64() > keepProb {
					continue
				}
			}
			text := strings.TrimSpace(p.Title + "\n" + p.Text)
			if text == "" {
				continue
			}
			id := sanitize(p.DocID)
			line := map[string]any{
				"type":      "user",
				"sessionId": id,
				"timestamp": base.Add(time.Duration(written) * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": text},
			}
			b, err := json.Marshal(line)
			if err != nil {
				continue
			}
			if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), append(b, '\n'), 0o644); err != nil {
				_ = gz.Close()
				_ = f.Close()
				return written, have, err
			}
			if gold[p.DocID] {
				have[p.DocID] = true
			}
			written++
		}
		_ = gz.Close()
		if err := f.Close(); err != nil {
			return written, have, err
		}
	}
	return written, have, nil
}

// MIRACL docids look like "12345#7"; '#' is not a safe filename or session id.
func sanitize(id string) string   { return strings.ReplaceAll(id, "#", "_") }
func unsanitize(id string) string { return strings.ReplaceAll(id, "_", "#") }

func readTopics(path string) (map[string]string, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = parts[1]
		order = append(order, parts[0])
	}
	return out, order, sc.Err()
}

func readQrels(path string) (map[string]map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] == "0" {
			continue
		}
		if out[fields[0]] == nil {
			out[fields[0]] = map[string]bool{}
		}
		out[fields[0]][fields[2]] = true
	}
	return out, sc.Err()
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func pct(a, n int) float64 {
	if n == 0 {
		return 0
	}
	return 100 * float64(a) / float64(n)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "miracl:", err)
	os.Exit(1)
}
