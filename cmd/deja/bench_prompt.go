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
		terms := prompt.Terms(chain.Question)
		fired, correct := promptBenchProbe(indexDir, chain.Project, chain.ID, terms)
		arm := &report.Real
		switch chain.Kind {
		case "marathon":
			arm = &report.Marathon
		case "fresh":
			arm = &report.Fresh
		default:
			realTerms = append(realTerms, len(terms))
		}
		arm.Cases++
		if fired {
			arm.Fired++
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
	ranked, matched, _, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil {
		return false, false
	}
	for i, s := range ranked {
		// Every gate the hook applies, applied here too — a benchmark that
		// skips one reports a recall the user would never see.
		if matched[i] < 1 || (matched[i] < 2 && !hasIdentifierTerm(terms)) {
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
