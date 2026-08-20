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

// absentSubjectQuestions ask about something this project has never held,
// using words it does hold. Measured on a real store: of the blocks that opened
// on a line carrying none of their terms, four of five were this — a question
// about a phone tether, a mobile hotspot, another project's tool, asked while
// sitting in this repository's directory. The subject appears nowhere in the
// project (0 sessions, 0 hits), the ordinary words appear everywhere, and the
// hook answers on those.
//
// The right answer is silence, and it is the one case tonight where firing
// less often is the improvement.
func absentSubjectQuestions() []string {
	// The subject belongs to another project; the rest is the vocabulary a
	// long working session is made of. That combination is the live shape,
	// found by printing the candidates rather than reasoning about them:
	//
	//   q     = which branch was the oauth rotation on when the suite passed
	//   terms = [branch oauth rotation suite passed]
	//   cand  = the 1000-message noise session, matched=3 strong=3
	//
	// The subject contributes nothing. "branch", "suite" and "passed" carry
	// the match, and the session that says them most often wins — which is
	// whichever session is longest. Two earlier drafts of these questions
	// measured something else: one joined the absent subject to a topic the
	// project really holds, so answering was reasonable, and one used words
	// that exist nowhere, where the hook already stays silent (0/3).
	return []string{
		"did the etag reuse ever break the branch during a suite run",
		"which branch was the oauth rotation on when the suite passed",
		"does the gzip streaming touch the branch the suite builds",
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
	// Questions in Russian, wrapped in the filler a person types around them.
	// The corpus was English-only, so nothing here spoke the language whose
	// filler list had just been extended — and over-filtering is the way that
	// change fails: put one subject word in the list by mistake and the
	// question falls silent. Measured: adding the three subjects to the filler
	// list takes this arm from 3/3 to 1/3.
	//
	// It does not see the defect that prompted the extension. That one was
	// about which line the block opens with, and every arm here scores which
	// session was chosen.
	Russian promptArmReport `json:"russian_questions"`
	// What the block actually shows. Every other arm scores which session was
	// chosen; none of them looks at the lines inside it, which is how a recall
	// that had found the right session came to open with "продолжай дальше"
	// and read as worthless. Correct here means the first line the agent sees
	// carries a word from the question.
	Shown promptArmReport `json:"shown_line"`
	// The decoy arm: the session that answers also says an ordinary word of the
	// question, three times, about something else. Correct means the block opens
	// on the line that carries the subject rather than the one that carries the
	// ordinary word.
	Decoy promptArmReport `json:"decoy_line"`
	// Questions whose subject the project has never held, asked with words it
	// has. Every fire is a false one: there is nothing to answer with.
	//
	// A target, not a guard. Today everything fires, so this arm reads 3/3
	// however the questions are written — swapping the absent subject for a
	// present one changes nothing. It earns its keep the day the number moves,
	// and until then it says how far there is to go.
	AbsentSubject promptArmReport `json:"absent_subject"`
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
		case "russian":
			arm = &report.Russian
		case "decoy":
			// Scored with the question's own terms, the way the hook builds the
			// block. An earlier version of this arm rebuilt it from the topic
			// alone and so could never see the decoy at all — it read 2/2 while
			// the block opened on "decide whether to decide this now".
			arm = &report.Decoy
			arm.Cases++
			if firstShownLineCarries(indexDir, scope, terms, chain.Topic) {
				arm.Fired++
				arm.Correct++
			}
			continue
		default:
			realTerms = append(realTerms, len(terms))
		}
		// What the block opens with, for the questions that have an answer to
		// open with. Scored apart from whether the right session was picked:
		// the two fail independently.
		if fired && (chain.Kind == "" || chain.Kind == "russian") {
			report.Shown.Cases++
			if shownLineCarriesATerm(indexDir, scope, terms) {
				report.Shown.Fired++
				report.Shown.Correct++
			}
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
	finishPromptArm(&report.Russian, nil)
	for _, q := range absentSubjectQuestions() {
		report.AbsentSubject.Cases++
		if fired, _ := promptBenchProbe(indexDir, bench.PromptHaystackProject, "no-such-chain", prompt.Terms(q)); fired {
			report.AbsentSubject.Fired++
			report.AbsentSubject.FalseFires++
		}
	}
	finishPromptArm(&report.Shown, nil)
	finishPromptArm(&report.Decoy, nil)
	finishPromptArm(&report.AbsentSubject, nil)
	return report, nil
}

// shownLineCarriesATerm builds the block the hook would inject and reports
// whether its first content line holds any of the query's terms. A block whose
// opening line came from the top of a long transcript does not, and that line
// is the whole frame an agent reads before deciding to ignore the rest.
func shownLineCarriesATerm(dir, project string, terms []string) bool {
	ranked, matched, strong, _, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil {
		return false
	}
	var keep []model.Session
	for i, s := range ranked {
		if !recallWorthShowing(terms, matched[i], strong[i]) {
			continue
		}
		keep = append(keep, s)
		break
	}
	if len(keep) == 0 {
		return false
	}
	block := search.AutoRecallDigestFor(keep, promptHookBudget-recallFrameOverhead, terms)
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		// The header line names the session; the first line under it is what
		// frames the block.
		if !strings.HasPrefix(line, "- User:") && !strings.HasPrefix(line, "- Assistant:") {
			continue
		}
		for _, t := range terms {
			// The same rule the block was built with, from the same function.
			if t != "" && search.TextCarriesTerm(line, t) {
				return true
			}
		}
		return false
	}
	return false
}

// firstShownLineCarries builds the block the hook would inject for this question
// and reports whether its first quoted line carries the subject rather than an
// ordinary word the question shares with the same session.
func firstShownLineCarries(dir, project string, terms []string, topic string) bool {
	ranked, matched, strong, idfOf, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil || len(ranked) == 0 {
		return false
	}
	// Ordered the way the hook orders them, so the arm scores the block the
	// product builds rather than one built from the raw query.
	terms = byIdentifying(terms, idfOf)
	var keep []model.Session
	for i := range ranked {
		if !recallWorthShowing(terms, matched[i], strong[i]) {
			continue
		}
		keep = append(keep, ranked[i])
		break
	}
	if len(keep) == 0 {
		return false
	}
	for _, ln := range strings.Split(search.AutoRecallDigestFor(keep, 2000, terms), "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "- User:") && !strings.HasPrefix(ln, "- Assistant:") {
			continue
		}
		return search.TextCarriesTerm(ln, topic)
	}
	return false
}

// promptBenchProbe runs the same two steps the hook runs: extract terms, then
// ask the index for this project's sessions ranked by them. Anything the hook
// would discard downstream is discarded here too, so the number reported is
// what a user would actually see.

func promptBenchProbe(dir, project, chainID string, terms []string) (fired, correct bool) {
	if !promptTermsWorthAsking(terms) {
		return false, false
	}
	ranked, matched, strong, _, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
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
