package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The per-prompt hook had no benchmark, so changes to its term extraction
// were unmeasurable: `bench recall` scores search and `bench context` scores
// the session-start digest, and neither runs promptSearchTerms.
//
// The questions here are phrased the way people ask them — short identifiers,
// filler words, sometimes another language — because that is exactly what the
// extractor has to survive. Negative controls ask about work the corpus does
// not contain: a run that fires on those is trading noise for coverage.
// negativeQuestions ask about work no chain contains. A run that fires on
// these is buying coverage with noise.
// offTopicQuestions join two topics the project really discussed into a
// question it never answered. Unlike the negative controls, every word here is
// present somewhere in the project; only the question is new.
func offTopicQuestions() []string {
	return []string{
		"did the kestrel timeout ever delay an escrow release",
		"do we write parquet before or after the kestrel handshake",
		"which escrow rows ended up in the parquet batch",
	}
}

func negativeQuestions() []string {
	return []string{
		"how do I bake sourdough bread at home",
		"what is the capital of Portugal",
		"explain quicksort to me",
		// The dangerous shape: short words that live in every session's
		// working noise. A floor low enough to keep "ttl" must not start
		// recalling on "the output".
		"can you run the tests again",
		"paste the output of that command",
		"walk me through the trace",
		"adjust the patch and try again",
		"add a log line here",
		"open the file and read it",
		"write the code for this",
	}
}

type promptArmReport struct {
	Cases      int     `json:"cases"`
	Fired      int     `json:"fired"`
	FireRate   float64 `json:"fire_rate"`
	Correct    int     `json:"correct"`
	Precision  float64 `json:"precision"`
	FalseFires int     `json:"false_fires"`
	MedianTerm float64 `json:"median_terms"`
}

type promptReport struct {
	CorpusHash string          `json:"corpus_hash"`
	Seed       int64           `json:"seed"`
	Real       promptArmReport `json:"real_questions"`
	Negative   promptArmReport `json:"negative_controls"`
	// Shapes the gates turn away: a session too long to read as one episode,
	// and one worked on minutes ago. Scored separately because the tradeoff
	// is different — here a miss is a question the user asked and did not
	// get answered.
	Marathon promptArmReport `json:"marathon_sessions"`
	Fresh    promptArmReport `json:"fresh_sessions"`
	// The bucket arm: a question asked while the project scope is a catch-all
	// that holds unrelated work and not the answer. Every fire here is a false
	// one — the right behaviour is silence, because a neighbour pulled out of
	// a catch-all is what teaches an agent to stop reading these injections.
	// Until this existed the benchmark handed the probe the correct project
	// every time, so it could not see a scope failure at all.
	Bucket promptArmReport `json:"bucket_scope"`
	// The haystack arm: three questions answered by three small sessions, in a
	// project that also holds one very long session mentioning all of them.
	// Correct means the small session won. Measured on a real store, one
	// session of 1476 messages answered 33 of 45 live prompts — a session that
	// touched everything matches everything, and narrowing it to the matching
	// part keeps it winning.
	Haystack promptArmReport `json:"haystack"`
	// The off-topic arm: a question built from words the project really uses,
	// about something nobody ever decided. Every fire is a false one. Measured
	// by hand on ten real prompts against a frozen index, five were answered
	// with plainly unrelated work and none with silence — the hook never says
	// it does not know, which is what teaches an agent to stop reading it.
	OffTopic promptArmReport `json:"off_topic"`
}

func runBenchPrompt(args []string) error {
	jsonOut, seed, err := parseBenchArgs("prompt", args)
	if err != nil {
		return err
	}
	report, err := measurePrompt(seed)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	fmt.Println("deja bench prompt")
	fmt.Printf("corpus: %d chains, seed %d\n", bench.ContextChainCount, report.Seed)
	fmt.Println("arm                fired  correct  precision")
	fmt.Printf("real questions     %2d/%-2d  %2d       %.2f\n",
		report.Real.Fired, report.Real.Cases, report.Real.Correct, report.Real.Precision)
	fmt.Printf("long sessions      %2d/%-2d  %2d       %.2f\n",
		report.Marathon.Fired, report.Marathon.Cases, report.Marathon.Correct, report.Marathon.Precision)
	fmt.Printf("recent sessions    %2d/%-2d  %2d       %.2f\n",
		report.Fresh.Fired, report.Fresh.Cases, report.Fresh.Correct, report.Fresh.Precision)
	fmt.Printf("negative controls  %2d/%-2d  —        false fires: %d\n",
		report.Negative.Fired, report.Negative.Cases, report.Negative.FalseFires)
	return nil
}

func measurePrompt(seed int64) (promptReport, error) {
	corpus := bench.GeneratePrompt(seed)
	root, err := benchmarkTempDir()
	if err != nil {
		return promptReport{}, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	claudeRoot := filepath.Join(root, "claude")
	indexDir := filepath.Join(root, "index.db")
	var sessions []model.Session
	for _, chain := range corpus.Chains {
		sessions = append(sessions, chain.Sessions...)
	}
	if err := writeBenchCorpus(claudeRoot, sessions); err != nil {
		return promptReport{}, err
	}
	restore := isolateBenchEnv(root, claudeRoot, indexDir)
	defer restore()
	if err := index.EnsureForSearch(indexDir, search.Options{Query: "", All: true}, true, io.Discard); err != nil {
		return promptReport{}, fmt.Errorf("build prompt benchmark index: %w", err)
	}
	report := promptReport{CorpusHash: corpus.Hash, Seed: seed}
	var realTerms, negTerms []int
	for _, chain := range corpus.Chains {
		if chain.Negative {
			continue
		}
		// Filler that only exists to fill the bucket has no question of its
		// own; it is asked about through the bucket-answer chain below.
		if chain.Kind == "bucket" || chain.Kind == "haystack-noise" {
			continue
		}
		terms := prompt.Terms(chain.Question)
		// The bucket question is asked against the catch-all scope rather than
		// against its own project — that is the whole point of the case, and
		// it is the only arm where the probe is not handed the right answer.
		scope := chain.Project
		if chain.Kind == "bucket-answer" {
			scope = bench.PromptBucketProject
		}
		fired, correct := promptBenchProbe(indexDir, scope, chain.ID, terms)
		arm := &report.Real
		switch chain.Kind {
		case "marathon":
			arm = &report.Marathon
		case "fresh":
			arm = &report.Fresh
		case "bucket-answer":
			arm = &report.Bucket
		case "haystack":
			arm = &report.Haystack
		default:
			realTerms = append(realTerms, len(terms))
		}
		arm.Cases++
		if fired {
			arm.Fired++
			// Nothing in the bucket can be the answer, so anything it returns
			// is a false fire.
			if chain.Kind == "bucket-answer" {
				arm.FalseFires++
			}
		}
		if correct {
			arm.Correct++
		}
	}
	// Negative questions are asked against every project in turn: firing
	// anywhere is a false fire.
	for _, q := range negativeQuestions() {
		terms := prompt.Terms(q)
		negTerms = append(negTerms, len(terms))
		report.Negative.Cases++
		for _, chain := range corpus.Chains {
			if fired, _ := promptBenchProbe(indexDir, chain.Project, chain.ID, terms); fired {
				report.Negative.Fired++
				report.Negative.FalseFires++
				break
			}
		}
	}
	finishPromptArm(&report.Real, realTerms)
	finishPromptArm(&report.Negative, negTerms)
	finishPromptArm(&report.Marathon, nil)
	finishPromptArm(&report.Fresh, nil)
	finishPromptArm(&report.Bucket, nil)
	// Asked against the project that holds every one of its words.
	for _, q := range offTopicQuestions() {
		report.OffTopic.Cases++
		if fired, _ := promptBenchProbe(indexDir, bench.PromptHaystackProject, "no-such-chain", prompt.Terms(q)); fired {
			report.OffTopic.Fired++
			report.OffTopic.FalseFires++
		}
	}
	finishPromptArm(&report.Haystack, nil)
	finishPromptArm(&report.OffTopic, nil)
	return report, nil
}

// promptBenchProbe runs the same two steps the hook runs: extract terms, then
// ask the index for this project's sessions ranked by them. Anything the hook
// would discard downstream is discarded here too, so the number reported is
// what a user would actually see.
func promptBenchProbe(dir, project, chainID string, terms []string) (fired, correct bool) {
	if !promptTermsWorthAsking(terms) {
		return false, false
	}
	ranked, matched, strong, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil {
		return false, false
	}
	for i, s := range ranked {
		// The same bar the hook applies, from the same function — kept in one
		// place because the two drifted: this one asked whether the query held
		// an identifier, the hook asked whether the session did.
		if !recallWorthShowing(terms, matched[i], strong[i]) {
			continue
		}
		if len(s.Messages) > dejaVuMaxMessages {
			if s = focusSession(s, terms); len(s.Messages) == 0 {
				continue
			}
		}
		fired = true
		if strings.HasPrefix(s.ID, chainID) {
			correct = true
		}
		break
	}
	return fired, correct
}

func finishPromptArm(arm *promptArmReport, terms []int) {
	if arm.Cases > 0 {
		arm.FireRate = float64(arm.Fired) / float64(arm.Cases)
	}
	if arm.Fired > 0 {
		arm.Precision = float64(arm.Correct) / float64(arm.Fired)
	}
	if len(terms) > 0 {
		arm.MedianTerm = percentileInt(terms, 50)
	}
}
