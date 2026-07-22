// Command longmemeval runs deja's production retrieval path over the
// LongMemEval-S benchmark (Wu et al., ICLR 2025) and reports session-level
// recall@1 / recall@5.
//
// Methodology, deliberately end-to-end: every question's haystack sessions are
// written as Claude-format transcript files, indexed by the same ingestion
// pipeline users run (parsing, redaction, tokenization), and queried with the
// verbatim question text through the same search ladder the CLI uses (exact →
// stem → fuzzy → co-occurrence). No question rewriting, no answer-aware
// tuning. Usage:
//
//	go run ./scripts/longmemeval -data longmemeval_s.json [-limit N] [-v]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

type lmeQuestion struct {
	QuestionID        string      `json:"question_id"`
	QuestionType      string      `json:"question_type"`
	Question          string      `json:"question"`
	QuestionDate      string      `json:"question_date"`
	HaystackDates     []string    `json:"haystack_dates"`
	HaystackSessionID []string    `json:"haystack_session_ids"`
	HaystackSessions  [][]lmeTurn `json:"haystack_sessions"`
	AnswerSessionIDs  []string    `json:"answer_session_ids"`
}

var hitCounts []int

type lmeTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func main() {
	dataPath := flag.String("data", "longmemeval_s.json", "path to longmemeval_s.json")
	limit := flag.Int("limit", 0, "run only the first N questions (0 = all)")
	skipAbs := flag.Bool("skip-abs", false, "skip abstention (_abs) questions, matching cleaned-dataset runs")
	verbose := flag.Bool("v", false, "log per-question results")
	flag.Parse()

	raw, err := os.ReadFile(*dataPath)
	if err != nil {
		fatal(err)
	}
	var questions []lmeQuestion
	if err := json.Unmarshal(raw, &questions); err != nil {
		fatal(err)
	}
	if *skipAbs {
		kept := questions[:0]
		for _, q := range questions {
			if !strings.Contains(q.QuestionID, "_abs") {
				kept = append(kept, q)
			}
		}
		questions = kept
	}
	if *limit > 0 && len(questions) > *limit {
		questions = questions[:*limit]
	}

	type bucket struct {
		n, r1, r5, r10, r20, miss int
		mrr                       float64
	}
	byType := map[string]*bucket{}
	total := &bucket{}
	var searchTimes []time.Duration
	var _ = hitCounts
	start := time.Now()

	for qi, q := range questions {
		rank, elapsed, err := runQuestion(q)
		if err != nil {
			fatal(fmt.Errorf("question %s: %w", q.QuestionID, err))
		}
		searchTimes = append(searchTimes, elapsed)
		b := byType[q.QuestionType]
		if b == nil {
			b = &bucket{}
			byType[q.QuestionType] = b
		}
		for _, bb := range []*bucket{b, total} {
			bb.n++
			if rank >= 1 {
				bb.mrr += 1 / float64(rank)
			}
			switch {
			case rank == 0:
				bb.miss++
			default:
				if rank <= 1 {
					bb.r1++
				}
				if rank <= 5 {
					bb.r5++
				}
				if rank <= 10 {
					bb.r10++
				}
				if rank <= 20 {
					bb.r20++
				}
			}
		}
		if *verbose {
			fmt.Printf("%4d/%d %-24s rank=%-3d %s\n", qi+1, len(questions), q.QuestionType, rank, q.QuestionID)
		}
	}

	sort.Slice(searchTimes, func(i, j int) bool { return searchTimes[i] < searchTimes[j] })
	sumHits := 0
	for _, h := range hitCounts {
		sumHits += h
	}
	avgHits := 0.0
	if len(hitCounts) > 0 {
		avgHits = float64(sumHits) / float64(len(hitCounts))
	}
	fmt.Printf("\nLongMemEval-S · deja production retrieval path (lexical ladder, no LLM, no embeddings)\n")
	fmt.Printf("questions: %d · wall: %s · median search: %s · avg candidates: %.1f\n\n", total.n, time.Since(start).Round(time.Second), searchTimes[len(searchTimes)/2].Round(time.Microsecond), avgHits)
	fmt.Printf("%-28s %6s %8s %8s %8s %8s %8s\n", "type", "n", "R@1", "R@5", "R@10", "R@20", "MRR")
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		b := byType[t]
		fmt.Printf("%-28s %6d %7.1f%% %7.1f%% %7.1f%% %7.1f%%   %.3f\n", t, b.n, pct(b.r1, b.n), pct(b.r5, b.n), pct(b.r10, b.n), pct(b.r20, b.n), b.mrr/float64(b.n))
	}
	fmt.Printf("%-28s %6d %7.1f%% %7.1f%% %7.1f%% %7.1f%%   %.3f\n", "TOTAL", total.n, pct(total.r1, total.n), pct(total.r5, total.n), pct(total.r10, total.n), pct(total.r20, total.n), total.mrr/float64(total.n))
}

// runQuestion builds a fresh index over the question's haystack via the real
// ingestion pipeline and returns the rank (1-based) of the first answer
// session in the search results, or 0 if absent from the top 50.
func runQuestion(q lmeQuestion) (int, time.Duration, error) {
	tmp, err := os.MkdirTemp("", "lme")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(tmp)
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-work-lme")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return 0, 0, err
	}
	// One transcript file per haystack session, in Claude Code's on-disk
	// format, timestamped with the haystack dates so freshness decay applies
	// exactly as it would on real history.
	for si, turns := range q.HaystackSessions {
		id := q.HaystackSessionID[si]
		ts := parseLMEDate(q.HaystackDates[si])
		f, err := os.Create(filepath.Join(proj, id+".jsonl"))
		if err != nil {
			return 0, 0, err
		}
		enc := json.NewEncoder(f)
		for ti, turn := range turns {
			// Claude's on-disk format: user content is a plain string,
			// assistant content is a list of typed blocks. Writing assistant
			// turns as strings makes the parser drop them entirely.
			var content any = turn.Content
			if turn.Role == "assistant" {
				content = []any{map[string]any{"type": "text", "text": turn.Content}}
			}
			line := map[string]any{
				"type":      turn.Role,
				"sessionId": id,
				"timestamp": ts.Add(time.Duration(ti) * time.Minute).UTC().Format(time.RFC3339),
				"message":   map[string]any{"role": turn.Role, "content": content},
			}
			if err := enc.Encode(line); err != nil {
				_ = f.Close()
				return 0, 0, err
			}
		}
		if err := f.Close(); err != nil {
			return 0, 0, err
		}
	}
	dir := filepath.Join(tmp, "index.db")
	_ = os.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	_ = os.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "claude", true, nil); err != nil {
		return 0, 0, err
	}

	o := search.Options{Query: q.Question, All: true}
	t0 := time.Now()
	result, err := index.SearchWithRecoveryDetailed(dir, o, nil)
	if err != nil {
		return 0, 0, err
	}
	o.Tier = result.Tier
	if result.Stemmed {
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
		o.FuzzyVariants = result.Variants
	}
	// Exactly the CLI/MCP code path: search.Run for exact/stem/fuzzy tiers,
	// order-preserving RelevanceHits when the ladder degraded to relevance.
	var hits []search.Hit
	if result.Tier == search.TierRelevance {
		hits = search.RelevanceHits(result.Sessions, index.RelevanceTerms(q.Question))
	} else if hits, err = search.Run(result.Sessions, o); err != nil {
		return 0, 0, err
	}
	ranked := make([]string, 0, 50)
	for _, h := range hits {
		ranked = append(ranked, h.Session.ID)
	}
	elapsed := time.Since(t0)
	hitCounts = append(hitCounts, len(ranked))
	want := map[string]bool{}
	for _, id := range q.AnswerSessionIDs {
		want[id] = true
	}
	for i, id := range ranked {
		if i >= 50 {
			break
		}
		if want[id] {
			return i + 1, elapsed, nil
		}
	}
	return 0, elapsed, nil
}

// parseLMEDate parses "2023/05/20 (Sat) 02:21".
func parseLMEDate(s string) time.Time {
	t, err := time.Parse("2006/01/02 (Mon) 15:04", s)
	if err != nil {
		return time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}

func pct(a, n int) float64 {
	if n == 0 {
		return 0
	}
	return 100 * float64(a) / float64(n)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "longmemeval:", err)
	os.Exit(1)
}
