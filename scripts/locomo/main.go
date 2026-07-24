// Command locomo runs deja's production retrieval path over the LoCoMo
// benchmark (Maharana et al., ACL 2024) and reports session-level recall.
//
// Methodology mirrors scripts/longmemeval: each dialog's sessions are written
// as Claude-format transcripts ("Speaker: text" turns), indexed by the normal
// ingestion pipeline, and every question is issued verbatim through the
// production search ladder. Gold sessions derive from the evidence turn ids
// (D<session>:<turn>). Category 5 is adversarial by design and reported
// separately. Usage:
//
//	go run ./scripts/locomo -data locomo10.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

type locomoSample struct {
	SampleID     string         `json:"sample_id"`
	QA           []locomoQA     `json:"qa"`
	Conversation map[string]any `json:"conversation"`
}

type locomoQA struct {
	Question string `json:"question"`
	Evidence any    `json:"evidence"`
	Category any    `json:"category"`
}

var evidenceRE = regexp.MustCompile(`D(\d+):\d+`)

func main() {
	dataPath := flag.String("data", "locomo10.json", "path to locomo10.json")
	dumpMisses := flag.String("dump-misses", "", "write a JSONL miss report (rank!=1) to this path")
	flag.Parse()
	var missFile *os.File
	if *dumpMisses != "" {
		var err error
		if missFile, err = os.Create(*dumpMisses); err != nil {
			fatal(err)
		}
		defer func() { _ = missFile.Close() }()
	}

	raw, err := os.ReadFile(*dataPath)
	if err != nil {
		fatal(err)
	}
	var samples []locomoSample
	if err := json.Unmarshal(raw, &samples); err != nil {
		fatal(err)
	}

	type bucket struct {
		n, r1, r5, miss int
		mrr             float64
	}
	byCat := map[string]*bucket{}
	total := &bucket{}
	start := time.Now()
	var searchTimes []time.Duration

	for _, sample := range samples {
		dir, cleanup, err := buildDialogIndex(sample)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", sample.SampleID, err))
		}
		for _, qa := range sample.QA {
			gold := map[string]bool{}
			for _, m := range evidenceRE.FindAllStringSubmatch(fmt.Sprint(qa.Evidence), -1) {
				gold["sess-"+m[1]] = true
			}
			if len(gold) == 0 {
				continue
			}
			rank, detail, elapsed, err := askDialog(dir, qa.Question, gold)
			if err != nil {
				fatal(err)
			}
			if missFile != nil && rank != 1 {
				goldIDs := make([]string, 0, len(gold))
				for g := range gold {
					goldIDs = append(goldIDs, g)
				}
				sort.Strings(goldIDs)
				rec := map[string]any{
					"sample": sample.SampleID, "category": fmt.Sprint(qa.Category),
					"question": qa.Question, "rank": rank, "tier": detail.tier,
					"gold": goldIDs, "top5": detail.top5,
				}
				bb, _ := json.Marshal(rec)
				_, _ = missFile.Write(append(bb, 10))
			}
			searchTimes = append(searchTimes, elapsed)
			cat := fmt.Sprint(qa.Category)
			b := byCat[cat]
			if b == nil {
				b = &bucket{}
				byCat[cat] = b
			}
			for _, bb := range []*bucket{b, total} {
				bb.n++
				if rank >= 1 {
					bb.mrr += 1 / float64(rank)
				}
				if rank == 0 {
					bb.miss++
				} else {
					if rank == 1 {
						bb.r1++
					}
					if rank <= 5 {
						bb.r5++
					}
				}
			}
		}
		cleanup()
	}

	sort.Slice(searchTimes, func(i, j int) bool { return searchTimes[i] < searchTimes[j] })
	fmt.Printf("\nLoCoMo · deja production retrieval path (session-level, lexical ladder, no LLM)\n")
	fmt.Printf("questions: %d · wall: %s · median search: %s\n\n", total.n, time.Since(start).Round(time.Second), searchTimes[len(searchTimes)/2].Round(time.Microsecond))
	names := map[string]string{"1": "multi-hop", "2": "temporal", "3": "open-domain", "4": "single-hop", "5": "adversarial*"}
	fmt.Printf("%-18s %6s %8s %8s %8s\n", "category", "n", "R@1", "R@5", "MRR")
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		b := byCat[c]
		fmt.Printf("%-18s %6d %7.1f%% %7.1f%%   %.3f\n", names[c], b.n, pct(b.r1, b.n), pct(b.r5, b.n), b.mrr/float64(b.n))
	}
	fmt.Printf("%-18s %6d %7.1f%% %7.1f%%   %.3f\n", "TOTAL", total.n, pct(total.r1, total.n), pct(total.r5, total.n), total.mrr/float64(total.n))
	fmt.Println("* adversarial questions are unanswerable by design; retrieval still locates the referenced session")
}

func buildDialogIndex(sample locomoSample) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "locomo")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-work-locomo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		cleanup()
		return "", nil, err
	}
	defaultBase := time.Date(2023, 5, 1, 10, 0, 0, 0, time.UTC)
	for k, v := range sample.Conversation {
		var sessNum string
		if _, err := fmt.Sscanf(k, "session_%s", &sessNum); err != nil || strings.Contains(sessNum, "_") {
			continue
		}
		// LoCoMo carries real per-session dates ("1:56 pm on 8 May, 2023");
		// writing them into the fixtures gives temporal questions the same
		// freshness signal real history has.
		base := defaultBase
		if raw, ok := sample.Conversation["session_"+sessNum+"_date_time"].(string); ok {
			if t, err := time.Parse("3:04 pm on 2 January, 2006", raw); err == nil {
				base = t
			}
		}
		turns, ok := v.([]any)
		if !ok {
			continue
		}
		id := "sess-" + sessNum
		f, err := os.Create(filepath.Join(proj, id+".jsonl"))
		if err != nil {
			cleanup()
			return "", nil, err
		}
		enc := json.NewEncoder(f)
		for ti, tAny := range turns {
			turn, _ := tAny.(map[string]any)
			speaker, _ := turn["speaker"].(string)
			text, _ := turn["text"].(string)
			if text == "" {
				continue
			}
			line := map[string]any{
				"type":      "user",
				"sessionId": id,
				"timestamp": base.Add(time.Duration(ti) * time.Minute).UTC().Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": speaker + ": " + text},
			}
			if err := enc.Encode(line); err != nil {
				_ = f.Close()
				cleanup()
				return "", nil, err
			}
		}
		if err := f.Close(); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	dir := filepath.Join(tmp, "index.db")
	_ = os.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	_ = os.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "claude", true, nil); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

type dialogDetail struct {
	tier string
	top5 []string
}

func askDialog(dir, question string, gold map[string]bool) (int, dialogDetail, time.Duration, error) {
	o := query.Options{Query: question, All: true}
	t0 := time.Now()
	result, err := index.SearchWithRecoveryDetailed(dir, o, nil)
	if err != nil {
		return 0, dialogDetail{}, 0, err
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
		hits = search.RelevanceHits(result.Sessions, index.RelevanceTerms(question))
	} else if hits, err = search.Run(result.Sessions, o); err != nil {
		return 0, dialogDetail{}, 0, err
	}
	elapsed := time.Since(t0)
	detail := dialogDetail{tier: string(result.Tier)}
	for i, h := range hits {
		if i >= 5 {
			break
		}
		detail.top5 = append(detail.top5, h.Session.ID)
	}
	for i, h := range hits {
		if i >= 20 {
			break
		}
		if gold[h.Session.ID] {
			return i + 1, detail, elapsed, nil
		}
	}
	return 0, detail, elapsed, nil
}

func pct(a, n int) float64 {
	if n == 0 {
		return 0
	}
	return 100 * float64(a) / float64(n)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "locomo:", err)
	os.Exit(1)
}
