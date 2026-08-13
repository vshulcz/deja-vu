// Command day0bench measures the first minute after installing on a machine
// that already has coding-agent history.
//
// Every other benchmark in this repo starts with a warm index over a corpus
// that is already there, and answers "how well does deja rank what it holds".
// This one answers the question deja is built around: the history predates the
// install, so what does a cold machine give you before any work is done.
//
// What it reports, per run:
//
//   - reach: sessions written to disk versus sessions the parsers actually
//     ingested, per harness. A parser that silently skips is the failure this
//     is most likely to expose, and it is invisible to a warm-index benchmark.
//   - build: wall time to go from no index to a queryable one.
//   - first answer: wall time of the first query after that build.
//   - hit@1 / hit@5 over questions whose answers exist only in that history.
//
// The control is the part that makes the rest mean anything: with -control the
// corpus is written and deja is pointed at an empty store, which is what a
// memory that only records forward would see on day zero. It must score zero.
// If it does not, the questions are answerable from something other than the
// history and every number here is inflated.
//
// Pass -ctx with a path to a ctx binary and it runs over the same corpus and
// is scored by the same rule, so the neighbouring column is a run anyone can
// repeat rather than a number quoted from somewhere.
//
//	go run ./scripts/day0bench -data longmemeval_s_cleaned.json [-limit N] [-control] [-ctx PATH]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

type question struct {
	ID            string   `json:"question_id"`
	Question      string   `json:"question"`
	AnswerSession []string `json:"answer_session_ids"`
	Sessions      [][]turn `json:"haystack_sessions"`
	SessionIDs    []string `json:"haystack_session_ids"`
	Dates         []string `json:"haystack_dates"`
	QuestionDate  string   `json:"question_date"`
}

type turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// writer lays a session down in one harness's real on-disk shape. Only formats
// that are plain files are here: opencode and cursor keep sessions in SQLite,
// and a synthetic database would test the fixture rather than the parser.
type writer struct {
	harness string
	env     string // the DEJA_*_ROOT that points deja at this corpus
	write   func(root, id string, ts time.Time, turns []turn) error
}

func writers() []writer {
	return []writer{
		{"claude", "DEJA_CLAUDE_ROOT", writeClaude},
		{"codex", "DEJA_CODEX_ROOT", writeCodex},
	}
}

func writeClaude(root, id string, ts time.Time, turns []turn) error {
	proj := filepath.Join(root, "-work-day0")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(proj, id+".jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, t := range turns {
		// Assistant content is a list of typed blocks on disk; writing it as a
		// string makes the parser drop the turn.
		var content any = t.Content
		if t.Role == "assistant" {
			content = []any{map[string]any{"type": "text", "text": t.Content}}
		}
		if err := enc.Encode(map[string]any{
			"type":      t.Role,
			"sessionId": id,
			"timestamp": ts.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": t.Role, "content": content},
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeCodex(root, id string, ts time.Time, turns []turn) error {
	dir := filepath.Join(root, "sessions", ts.UTC().Format("2006/01/02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "rollout-"+id+".jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, t := range turns {
		if err := enc.Encode(map[string]any{
			"timestamp": ts.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339),
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "message",
				"role":    t.Role,
				"content": []any{map[string]any{"type": "input_text", "text": t.Content}},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

type runResult struct {
	written, indexed int
	build, first     time.Duration
	// found counts the answer session appearing anywhere in the top depthK,
	// which separates the two ways a query fails: ranked badly, or never
	// retrieved at all. Only the second one is fixed by ranking work.
	hit1, hit5, found, n int
}

// depthK bounds both sides' result list, so found means the same thing in
// every row.
const depthK = 50

func main() {
	data := flag.String("data", "", "LongMemEval-shaped corpus to lay down as history")
	limit := flag.Int("limit", 20, "questions to run")
	control := flag.Bool("control", false, "write the corpus but read an empty store: must score zero")
	ctxBin := flag.String("ctx", "", "path to a ctx binary to run over the same corpus, scored the same way")
	misses := flag.Bool("misses", false, "print the questions whose answer session never came back, for triage")
	corpus := flag.Int("corpus", 0, "lay down history from this many questions while scoring -limit of them; holds the questions fixed so only the pile grows")
	flag.Parse()
	if *data == "" {
		fmt.Fprintln(os.Stderr, "day0bench: -data is required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "day0bench:", err)
		os.Exit(1)
	}
	var qs []question
	if err := json.Unmarshal(raw, &qs); err != nil {
		fmt.Fprintln(os.Stderr, "day0bench:", err)
		os.Exit(1)
	}
	// The corpus and the scored set are separate knobs. Growing -limit alone
	// grows the pile and swaps in different questions at the same time, so a
	// drop could be either one; holding the questions still and moving -corpus
	// asks only whether more history makes the same question harder.
	all := qs
	if *corpus > 0 && len(all) > *corpus {
		all = all[:*corpus]
	}
	if *limit > 0 && len(qs) > *limit {
		qs = qs[:*limit]
	}
	if *corpus <= 0 {
		all = qs
	}

	for _, w := range writers() {
		r, err := run(w, all, qs, *control, *misses)
		if err != nil {
			fmt.Fprintf(os.Stderr, "day0bench %s: %v\n", w.harness, err)
			os.Exit(1)
		}
		report(w.harness, r)
	}
	if *ctxBin != "" {
		r, err := runCtx(*ctxBin, all, qs, *control)
		if err != nil {
			fmt.Fprintln(os.Stderr, "day0bench ctx:", err)
			os.Exit(1)
		}
		report("ctx", r)
		// Said plainly here because the number is easy to misread: deja is timed
		// in-process and ctx is timed as a subprocess, so its first answer
		// carries process start and a freshness check that deja's does not.
		// That is the wait a person at a terminal gets, not engine speed.
		fmt.Println("\nnote: ctx is timed as a subprocess, deja in-process — first-answer times are not engine-to-engine")
	}
	if *control {
		fmt.Println("\ncontrol: every hit above should be 0 — a memory that starts empty knows none of this")
	}
}

// report prints one row. Both sides go through it so the columns cannot drift
// apart into two different definitions of the same word.
func report(name string, r runResult) {
	reach := 0.0
	if r.written > 0 {
		reach = float64(r.indexed) / float64(r.written) * 100
	}
	fmt.Printf("%-8s reach %d/%d (%.1f%%)  build %s  first answer %s  hit@1 %d/%d  hit@5 %d/%d  found@%d %d/%d\n",
		name, r.indexed, r.written, reach,
		r.build.Round(time.Millisecond), r.first.Round(time.Millisecond),
		r.hit1, r.n, r.hit5, r.n, depthK, r.found, r.n)
}

func run(w writer, all, qs []question, control bool, misses bool) (runResult, error) {
	var out runResult
	tmp, err := os.MkdirTemp("", "day0")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	root := filepath.Join(tmp, w.harness)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return out, err
	}

	// One shared corpus for every question, laid down once: this is a machine
	// with months of history, not one haystack per query.
	seen := map[string]bool{}
	for _, q := range all {
		for i, turns := range q.Sessions {
			id := q.SessionIDs[i]
			if seen[id] {
				continue
			}
			seen[id] = true
			if err := w.write(root, id, parseDate(q.Dates[i]), turns); err != nil {
				return out, err
			}
			out.written++
		}
	}

	dir := filepath.Join(tmp, "index.db")
	// The control points deja at a directory with no history in it, which is
	// what a forward-only memory sees on a machine it was just installed on.
	readRoot := root
	if control {
		readRoot = filepath.Join(tmp, "empty")
		if err := os.MkdirAll(readRoot, 0o755); err != nil {
			return out, err
		}
	}
	_ = os.Setenv(w.env, readRoot)
	_ = os.Setenv("DEJA_INDEX_DIR", dir)

	t0 := time.Now()
	if err := index.Ensure(dir, w.harness, true, nil); err != nil {
		return out, err
	}
	out.build = time.Since(t0)

	if out.indexed, err = index.SessionCount(dir); err != nil {
		return out, err
	}

	for i, q := range qs {
		o := search.Options{Query: q.Question, All: true}
		if t, err := time.Parse("2006/01/02 (Mon) 15:04", q.QuestionDate); err == nil {
			o.Now = t
		}
		t1 := time.Now()
		result, err := index.SearchWithRecoveryDetailed(dir, o, nil)
		if err != nil {
			return out, err
		}
		o.Tier = result.Tier
		if result.Stemmed {
			o.Stemmed = true
			o.FuzzyVariants = result.Variants
		} else if result.Fuzzy {
			o.FuzzyVariants = result.Variants
		}
		var hits []search.Hit
		switch result.Tier {
		case search.TierError:
			hits = search.ErrorHits(result.Sessions)
		case search.TierRelevance:
			hits = search.RelevanceHits(result.Sessions, index.RelevanceTerms(q.Question))
		default:
			if hits, err = search.Run(result.Sessions, o); err != nil {
				return out, err
			}
		}
		if i == 0 {
			out.first = time.Since(t1)
		}
		want := map[string]bool{}
		for _, id := range q.AnswerSession {
			want[id] = true
		}
		out.n++
		hit := false
		if len(hits) > depthK {
			hits = hits[:depthK]
		}
		for rank, h := range hits {
			if !want[h.Session.ID] {
				continue
			}
			if rank == 0 {
				out.hit1++
			}
			if rank < 5 {
				out.hit5++
			}
			out.found++
			hit = true
			break
		}
		// A question whose answer session never came back at all is a retrieval
		// miss, not a ranking one, and no amount of reordering reaches it.
		if !hit && misses {
			fmt.Printf("  miss %s: %s\n", q.ID, q.Question)
		}
	}
	return out, nil
}

func parseDate(s string) time.Time {
	for _, layout := range []string{"2006/01/02 (Mon) 15:04", "2006/01/02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now().AddDate(0, -3, 0)
}
