package main

// Scoring somebody else's retrieval.
//
// The driver in main.go measures deja by running deja. Nobody else can run
// that, so every comparison between memory tools is each project quoting its
// own number from its own harness — which is exactly the state the independent
// review at neoneye/agent-memory-atlas complained about, and complaining back
// with another self-reported figure does not fix it.
//
// A submission file does: question id to the session ids a system ranked, in
// order. Whatever produced that ranking — embeddings, an LLM rewriting the
// query, a graph, grep — is the other project's business, and the numbers come
// out of this file's arithmetic rather than anyone's README.
//
//	go run ./scripts/longmemeval -data longmemeval_s.json -score theirs.json
//
// The dataset digest is printed with the table, because a number is only
// comparable against the same questions.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/vshulcz/deja-vu/scripts/benchresult"
)

// submission is what a system hands in: ranked session ids per question. The
// two shapes people will write are accepted, because refusing a file over a
// wrapper key wastes their afternoon and teaches nothing:
//
//	{"<question-id>": ["ses_a", "ses_b"]}
//	{"answers": {"<question-id>": ["ses_a", "ses_b"]}}
type submission map[string][]string

func readSubmission(path string) (submission, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var flat submission
	if err := json.Unmarshal(b, &flat); err == nil && len(flat) > 0 {
		return flat, nil
	}
	var wrapped struct {
		Answers submission `json:"answers"`
	}
	if err := json.Unmarshal(b, &wrapped); err != nil {
		return nil, fmt.Errorf("%s is neither {question_id: [ids]} nor {\"answers\": {…}}: %w", path, err)
	}
	if len(wrapped.Answers) == 0 {
		return nil, fmt.Errorf("%s holds no answers", path)
	}
	return wrapped.Answers, nil
}

// rankIn is the rank of the first evidence session in a ranked list, 1-based,
// and 0 when none of them is there — the same rule the deja path uses, so the
// two columns mean the same thing.
func rankIn(ranked []string, evidence []string) int {
	want := map[string]bool{}
	for _, id := range evidence {
		want[id] = true
	}
	for i, id := range ranked {
		if want[id] {
			return i + 1
		}
	}
	return 0
}

// evidenceRecallIn is the stricter per-evidence metric: the share of a
// question's evidence sessions that appear in the top k.
func evidenceRecallIn(ranked []string, evidence []string, k int) float64 {
	if len(evidence) == 0 {
		return 0
	}
	top := map[string]bool{}
	for i, id := range ranked {
		if i >= k {
			break
		}
		top[id] = true
	}
	found := 0
	for _, id := range evidence {
		if top[id] {
			found++
		}
	}
	return float64(found) / float64(len(evidence))
}

// scoreSubmission prints the same table the deja run prints, from a file
// instead of from a search. Missing questions are reported rather than skipped:
// a submission that answers half the set and scores well on that half is the
// most common way a benchmark gets misread.
func scoreSubmission(path, dataPath string, questions []lmeQuestion, out string) {
	sub, err := readSubmission(path)
	if err != nil {
		fatal(err)
	}
	start := time.Now()
	byType := map[string]*bucket{}
	total := &bucket{}
	evSum := map[int]float64{}
	evN := 0
	var missing []string

	for _, q := range questions {
		ranked, ok := sub[q.QuestionID]
		if !ok {
			missing = append(missing, q.QuestionID)
		}
		rank := rankIn(ranked, q.AnswerSessionIDs)
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
				continue
			}
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
		for _, k := range []int{1, 5, 10, 20} {
			evSum[k] += evidenceRecallIn(ranked, q.AnswerSessionIDs, k)
		}
		evN++
	}

	ds, err := benchresult.DescribeDataset(dataPath)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\nLongMemEval-S · submitted ranking from %s\n", path)
	fmt.Printf("questions: %d · dataset %s · sha256 %s\n", total.n, ds.Name, ds.SHA256)
	if len(missing) > 0 {
		fmt.Printf("unanswered: %d of %d questions carry no ranking in the file", len(missing), total.n)
		if len(missing) <= 3 {
			fmt.Printf(" (%v)", missing)
		}
		fmt.Printf(" — counted as misses\n")
	}
	fmt.Println()
	fmt.Printf("%-28s %6s %8s %8s %8s %8s %8s\n", "type", "n", "hit@1", "hit@5", "hit@10", "hit@20", "MRR")
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		b := byType[t]
		fmt.Printf("%-28s %6d %7.1f%% %7.1f%% %7.1f%% %7.1f%%   %.3f\n", t, b.n,
			pct(b.r1, b.n), pct(b.r5, b.n), pct(b.r10, b.n), pct(b.r20, b.n), b.mrr/float64(b.n))
	}
	fmt.Printf("%-28s %6d %7.1f%% %7.1f%% %7.1f%% %7.1f%%   %.3f\n", "TOTAL", total.n,
		pct(total.r1, total.n), pct(total.r5, total.n), pct(total.r10, total.n), pct(total.r20, total.n),
		total.mrr/float64(total.n))
	if evN > 0 {
		fmt.Printf("%-28s %6s %7.1f%% %7.1f%% %7.1f%% %7.1f%%\n", "evidence-recall (official)", "",
			100*evSum[1]/float64(evN), 100*evSum[5]/float64(evN), 100*evSum[10]/float64(evN),
			100*evSum[20]/float64(evN))
	}

	if out != "" {
		var times []time.Duration
		writeSubmissionResult(out, path, ds, total, byType, types, start, evSum, evN, times)
	}
}

// writeSubmissionResult saves a submitted run in the same shape as ours, so a
// project publishing its number publishes the same artifact rather than a
// sentence in a README.
func writeSubmissionResult(out, from string, ds benchresult.Dataset, total *bucket,
	byType map[string]*bucket, types []string, start time.Time,
	evSum map[int]float64, evN int, _ []time.Duration) {
	row := func(name string, b *bucket) benchresult.Row {
		h10, h20 := pct(b.r10, b.n), pct(b.r20, b.n)
		return benchresult.Row{Name: name, N: b.n, Hit1: pct(b.r1, b.n), Hit5: pct(b.r5, b.n),
			Hit10: &h10, Hit20: &h20, MRR: b.mrr / float64(b.n)}
	}
	res := benchresult.Result{
		Benchmark:   "LongMemEval-S",
		Harness:     "scripts/longmemeval -score",
		Dataset:     ds,
		Flags:       map[string]any{"submission": from},
		Questions:   total.n,
		WallSeconds: time.Since(start).Seconds(),
		Total:       row("TOTAL", total),
		Notes: map[string]string{
			"metric":   "hit@k credits a question when any of its evidence sessions ranks in the top k",
			"official": "evidence_recall is the stricter per-evidence metric the dataset defines",
			"source":   "ranking submitted by another system; deja did not run this retrieval",
		},
	}
	for _, t := range types {
		res.Rows = append(res.Rows, row(t, byType[t]))
	}
	if evN > 0 {
		res.Extra = map[string]any{"evidence_recall": map[string]float64{
			"@1":  100 * evSum[1] / float64(evN),
			"@5":  100 * evSum[5] / float64(evN),
			"@10": 100 * evSum[10] / float64(evN),
			"@20": 100 * evSum[20] / float64(evN),
		}}
	}
	if err := benchresult.Write(out, res); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s\n", out)
}
