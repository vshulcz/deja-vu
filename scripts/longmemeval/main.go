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
	"github.com/vshulcz/deja-vu/internal/prompt"
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
	dumpMisses := flag.String("dump-misses", "", "write a JSONL miss report (rank!=1) to this path")
	hookPrecision := flag.Int("hook-precision", 0, "measure the auto-recall hook gate on N cross-paired prompts (0 = off)")
	precision := flag.Bool("precision", false, "measure false-positive recalls: pair each question's prompt with another question's haystack (no answer present) and report how often anything surfaces")
	agentCases := flag.Int("agent-cases", 0, "dump N cases where the answer is in top-5 but not rank-1, for an agent-choice A/B")
	flag.Parse()
	evSum := map[int]float64{}
	evN := 0
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
	if *hookPrecision > 0 {
		runHookPrecision(questions, *hookPrecision)
		return
	}
	if *precision {
		runPrecision(questions)
		return
	}
	if *agentCases > 0 {
		runAgentCases(questions, *agentCases)
		return
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
		rank, detail, elapsed, err := runQuestion(q)
		for k, v := range detail.evRecall {
			evSum[k] += v
		}
		evN++
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
			if rank == 0 {
				bb.miss++
			} else {
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
		if missFile != nil && rank != 1 {
			rec := map[string]any{
				"question_id": q.QuestionID,
				"type":        q.QuestionType,
				"question":    q.Question,
				"rank":        rank,
				"tier":        detail.tier,
				"answer_ids":  q.AnswerSessionIDs,
				"top10":       detail.top10,
			}
			b, _ := json.Marshal(rec)
			_, _ = missFile.Write(append(b, '\n'))
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
	fmt.Printf("%-28s %6s %8s %8s %8s %8s %8s\n", "type", "n", "hit@1", "hit@5", "hit@10", "hit@20", "MRR")
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
	if evN > 0 {
		fmt.Printf("%-28s %6s %7.1f%% %7.1f%% %7.1f%% %7.1f%%\n", "evidence-recall (official)", "", 100*evSum[1]/float64(evN), 100*evSum[5]/float64(evN), 100*evSum[10]/float64(evN), 100*evSum[20]/float64(evN))
	}
}

type questionDetail struct {
	tier     string
	top10    []string
	topCount int
	cands    []map[string]any
	// evidence recall: how many of the question's answer sessions appear in
	// the top-k, divided by how many exist — LongMemEval's official metric,
	// stricter than any-hit on multi-evidence questions.
	evRecall map[int]float64
}

// runQuestion builds a fresh index over the question's haystack via the real
// ingestion pipeline and returns the rank (1-based) of the first answer
// session in the search results, or 0 if absent from the top 50.
func runQuestion(q lmeQuestion) (int, questionDetail, time.Duration, error) {
	tmp, err := os.MkdirTemp("", "lme")
	if err != nil {
		return 0, questionDetail{}, 0, err
	}
	defer os.RemoveAll(tmp)
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-work-lme")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return 0, questionDetail{}, 0, err
	}
	// One transcript file per haystack session, in Claude Code's on-disk
	// format, timestamped with the haystack dates so freshness decay applies
	// exactly as it would on real history.
	for si, turns := range q.HaystackSessions {
		id := q.HaystackSessionID[si]
		ts := parseLMEDate(q.HaystackDates[si])
		f, err := os.Create(filepath.Join(proj, id+".jsonl"))
		if err != nil {
			return 0, questionDetail{}, 0, err
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
				return 0, questionDetail{}, 0, err
			}
		}
		if err := f.Close(); err != nil {
			return 0, questionDetail{}, 0, err
		}
	}
	dir := filepath.Join(tmp, "index.db")
	_ = os.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	_ = os.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "claude", true, nil); err != nil {
		return 0, questionDetail{}, 0, err
	}

	o := search.Options{Query: q.Question, All: true}
	if t, err := time.Parse("2006/01/02 (Mon) 15:04", q.QuestionDate); err == nil {
		o.Now = t
	}
	t0 := time.Now()
	result, err := index.SearchWithRecoveryDetailed(dir, o, nil)
	if err != nil {
		return 0, questionDetail{}, 0, err
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
	if result.Tier == search.TierError {
		hits = search.ErrorHits(result.Sessions)
	} else if result.Tier == search.TierRelevance {
		hits = search.RelevanceHits(result.Sessions, index.RelevanceTerms(q.Question))
	} else if hits, err = search.Run(result.Sessions, o); err != nil {
		return 0, questionDetail{}, 0, err
	}
	ranked := make([]string, 0, 50)
	for _, h := range hits {
		ranked = append(ranked, h.Session.ID)
	}
	elapsed := time.Since(t0)
	hitCounts = append(hitCounts, len(ranked))
	detail := questionDetail{tier: string(result.Tier)}
	if len(hits) > 0 {
		detail.topCount = hits[0].Count
	}
	if len(ranked) > 10 {
		detail.top10 = ranked[:10]
	} else {
		detail.top10 = ranked
	}
	want := map[string]bool{}
	for _, id := range q.AnswerSessionIDs {
		want[id] = true
	}
	for i := 0; i < len(hits) && i < 5; i++ {
		snip := ""
		if len(hits[i].Snippets) > 0 {
			snip = hits[i].Snippets[0]
		}
		detail.cands = append(detail.cands, map[string]any{"n": i + 1, "id": hits[i].Session.ID, "is_answer": want[hits[i].Session.ID], "snippet": snip})
	}
	detail.evRecall = map[int]float64{}
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
		detail.evRecall[k] = float64(got) / float64(len(want))
	}
	for i, id := range ranked {
		if i >= 50 {
			break
		}
		if want[id] {
			return i + 1, detail, elapsed, nil
		}
	}
	return 0, detail, elapsed, nil
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

// runPrecision measures false-positive recalls. It pairs each question's prompt
// with a DIFFERENT question's haystack, so the answer session is never present:
// a well-behaved retrieval either returns nothing or only a low-confidence
// (relevance-tier) guess. It reports how often anything surfaces at all, and how
// often an exact/close tier fired — the recalls a reader is most likely to trust
// and be misled by. Lower is better; this is the precision side the recall
// numbers cannot see.
func runPrecision(questions []lmeQuestion) {
	n := len(questions)
	if n < 2 {
		fatal(fmt.Errorf("need at least 2 questions for precision pairing"))
	}
	var surfaced, exactish int
	byTier := map[string]int{}
	strengthHist := map[string]int{}
	start := time.Now()
	for i := range questions {
		j := (i + 1) % n
		hybrid := lmeQuestion{
			QuestionID:        questions[i].QuestionID + "|hay:" + questions[j].QuestionID,
			QuestionType:      questions[i].QuestionType,
			Question:          questions[i].Question,
			QuestionDate:      questions[i].QuestionDate,
			HaystackDates:     questions[j].HaystackDates,
			HaystackSessionID: questions[j].HaystackSessionID,
			HaystackSessions:  questions[j].HaystackSessions,
			AnswerSessionIDs:  nil,
		}
		_, detail, _, err := runQuestion(hybrid)
		if err != nil {
			fatal(fmt.Errorf("precision pair %d: %w", i, err))
		}
		if len(detail.top10) > 0 {
			surfaced++
			byTier[detail.tier]++
			if detail.tier != "relevance" {
				exactish++
			}
			switch {
			case detail.topCount <= 1:
				strengthHist["count<=1"]++
			case detail.topCount <= 3:
				strengthHist["count 2-3"]++
			case detail.topCount <= 6:
				strengthHist["count 4-6"]++
			default:
				strengthHist["count 7+"]++
			}
		}
	}
	fmt.Printf("LongMemEval-S · PRECISION (prompt_i × haystack_{i+1}, answer never present)\n")
	fmt.Printf("pairs: %d · wall: %s\n\n", n, time.Since(start).Round(time.Second))
	fmt.Printf("surfaced anything     %5d / %d = %.1f%%   (ideal: low)\n", surfaced, n, 100*float64(surfaced)/float64(n))
	fmt.Printf("of those, exact/close %5d / %d = %.1f%%   (most misleading)\n", exactish, n, 100*float64(exactish)/float64(n))
	fmt.Printf("\nby tier of the surfaced recall:\n")
	for t, c := range byTier {
		fmt.Printf("  %-12s %d\n", t, c)
	}
	fmt.Printf("\nstrength (match count) of the surfaced top hit:\n")
	for _, k := range []string{"count<=1", "count 2-3", "count 4-6", "count 7+"} {
		if strengthHist[k] > 0 {
			fmt.Printf("  %-10s %d\n", k, strengthHist[k])
		}
	}
}

// runAgentCases dumps cases where deja found the answer session in the top 5 but
// ranked it below #1 — exactly the cases a human/agent could rescue by choosing
// among the excerpts. Each case prints the question and the five candidate
// snippets (unlabelled), plus the 1-based position of the true answer for
// scoring. This is the raw material for the "let the agent pick" A/B.
func runAgentCases(questions []lmeQuestion, n int) {
	dumped := 0
	for _, q := range questions {
		rank, detail, _, err := runQuestion(q)
		if err != nil || rank < 2 || rank > 5 {
			continue
		}
		answerN := 0
		for _, c := range detail.cands {
			if c["is_answer"] == true {
				answerN = c["n"].(int)
			}
		}
		if answerN == 0 {
			continue
		}
		rec := map[string]any{
			"question":   q.Question,
			"type":       q.QuestionType,
			"deja_rank":  rank,
			"answer_pos": answerN,
			"candidates": detail.cands,
		}
		b, _ := json.Marshal(rec)
		fmt.Println(string(b))
		dumped++
		if dumped >= n {
			return
		}
	}
}

// buildHaystackIndex writes a question's haystack as Claude transcripts and
// indexes it, returning the index dir. Shared by the search and hook probes so
// both measure the same ingestion the CLI runs.
func buildHaystackIndex(q lmeQuestion) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "lmehook")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	proj := filepath.Join(tmp, "claude", "-work-lme")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return "", cleanup, err
	}
	for si, turns := range q.HaystackSessions {
		id := q.HaystackSessionID[si]
		ts := parseLMEDate(q.HaystackDates[si])
		f, err := os.Create(filepath.Join(proj, id+".jsonl"))
		if err != nil {
			return "", cleanup, err
		}
		enc := json.NewEncoder(f)
		for ti, turn := range turns {
			var content any = turn.Content
			if turn.Role == "assistant" {
				content = []any{map[string]any{"type": "text", "text": turn.Content}}
			}
			if err := enc.Encode(map[string]any{
				"type": turn.Role, "sessionId": id,
				"timestamp": ts.Add(time.Duration(ti) * time.Minute).UTC().Format(time.RFC3339),
				"message":   map[string]any{"role": turn.Role, "content": content},
			}); err != nil {
				_ = f.Close()
				return "", cleanup, err
			}
		}
		if err := f.Close(); err != nil {
			return "", cleanup, err
		}
	}
	dir := filepath.Join(tmp, "index.db")
	_ = os.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	_ = os.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "claude", true, nil); err != nil {
		return "", cleanup, err
	}
	return dir, cleanup, nil
}

// runHookPrecision measures the UNPROMPTED path: the auto-recall hook fires on
// every user message, so its false-positive rate is paid on every message.
// Each prompt is paired with another question's haystack — the answer is never
// present — and the probe reports how often the hook's gate would still inject.
// It replicates the gate rather than calling the hook: ProjectRelevant with the
// prompt's terms, then the matched-count bar the hook applies.
func runHookPrecision(questions []lmeQuestion, limit int) {
	if limit > 0 && limit < len(questions) {
		questions = questions[:limit]
	}
	n := len(questions)
	var wouldInject, oneTerm, twoPlus int
	start := time.Now()
	for i := range questions {
		j := (i + 1) % n
		dir, cleanup, err := buildHaystackIndex(questions[j])
		if err != nil {
			cleanup()
			fatal(err)
		}
		// The hook's own extraction, not the ranking's. They differ by the
		// identifier test and a six-term cap, so measuring the gate on
		// relevance terms measured a rule that never runs.
		terms := prompt.Terms(questions[i].Question)
		_, matched, strong, _, err := index.ProjectRelevant(dir, nil, terms, prompt.Candidates)
		if err != nil {
			cleanup()
			fatal(err)
		}
		best, bestStrong := 0, 0
		for k, m := range matched {
			if m > best {
				best = m
			}
			if k < len(strong) && strong[k] > bestStrong {
				bestStrong = strong[k]
			}
		}
		switch {
		case best >= 2:
			twoPlus++
			wouldInject++
		case best == 1:
			oneTerm++
			if bestStrong >= 1 {
				wouldInject++ // a rare term still earns a single-match inject
			}
		}
		cleanup()
	}
	fmt.Printf("auto-recall HOOK precision (prompt_i × haystack_{i+1}, answer never present)\n")
	fmt.Printf("pairs: %d · wall: %s\n\n", n, time.Since(start).Round(time.Second))
	fmt.Printf("would inject (current gate) %4d / %d = %.1f%%\n", wouldInject, n, 100*float64(wouldInject)/float64(n))
	fmt.Printf("  on ONE informative term %4d / %d = %.1f%%   (the bar a rarity test would raise)\n", oneTerm, n, 100*float64(oneTerm)/float64(n))
	fmt.Printf("  on TWO or more          %4d / %d = %.1f%%   (would still inject)\n", twoPlus, n, 100*float64(twoPlus)/float64(n))
}
