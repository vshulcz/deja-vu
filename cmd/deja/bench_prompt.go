package main

import (
	"bytes"
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
		// The same shape in Russian: an imperative that opens a message and
		// names nothing. Measured on a real store, "продолжай DDD-рефактор"
		// matched a session on "продолжай" and showed "продолжай и не отключай
		// больше streisand"; five messages of 142 got a block whose only terms
		// were words like these.
		"продолжай",
		"хорошо, продолжи",
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
	// Paraphrase asks each chain's question in words the fixture never used —
	// the shape of a person coming back a month later. Every other arm asks
	// with the fixture's own wording, so the gap between this and
	// real_questions is the vocabulary mismatch, which had no instrument until
	// it was measured by hand on a live store that had absorbed the questions
	// being asked of it (#2120).
	Paraphrase promptArmReport `json:"paraphrase"`
	// Tied is the same question in someone else's words, on a store that also
	// holds the sentence tying those words to the term of art. Separate from
	// Paraphrase because that arm has a ceiling: measured over the corpus, no
	// message anywhere says both vocabularies for any of the five it fails,
	// so nothing lexical can reach them. This arm is the reachable half, and
	// what a working bridge (#2331) would move.
	Tied promptArmReport `json:"paraphrase_tied"`
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
	// The decision arm: a session says its subject in passing before it
	// concludes anything about it, in Russian. Correct means the block carries
	// what was concluded rather than the line that put it off — the markers
	// that tell one from the other are English, and half the sessions on a real
	// store are not.
	Decision promptArmReport `json:"decision_line"`
	// The inline-decision arm: the passing mentions and the conclusion are in
	// one message. Correct means the block quotes the conclusion rather than
	// the denser line above it.
	DecisionInline promptArmReport `json:"decision_inline"`
	// The short-subject arm: the question names its subject in two characters,
	// the way people name a version or a pull request. Nothing else in the
	// question identifies anything, so dropping the subject leaves a working
	// verb and the answer is never found.
	ShortSubject promptArmReport `json:"short_subject"`
	// The echo arm: the session holds the user's own earlier copy of the very
	// sentence being typed now. Correct means the block opens on what was
	// concluded rather than handing the question back.
	Echo promptArmReport `json:"echo_line"`
	// The compound arm: a hyphenated subject whose parts are short. The floor
	// for a single Cyrillic word is three letters; a compound needed five in one
	// part, so ordinary names fell through.
	Compound promptArmReport `json:"compound_subject"`
	// The end-to-end arm: the hook itself, not the copy of its loop this file
	// carries. Every other arm reaches into the index directly, so a change that
	// lives in the hook — the order of the gates, what the block opens with,
	// whether a pointer goes out — moves no number here at all.
	EndToEnd promptArmReport `json:"hook_end_to_end"`
	// The tool-only arm: another session holds the subject, but every mention
	// of it was printed by a tool. Nothing there answers the question, so
	// correct means the hook stays silent.
	ToolOnly promptArmReport `json:"tool_only"`
	// The concluded arm: two sessions hold the subject, one only mentioned it
	// and the other settled it. Correct means the block carries what was
	// settled.
	Concluded promptArmReport `json:"concluded_session"`
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
	fmt.Printf("reworded           %2d/%-2d  %2d       %.2f\n",
		report.Paraphrase.Fired, report.Paraphrase.Cases, report.Paraphrase.Correct, report.Paraphrase.Precision)
	fmt.Printf("reworded, tied     %2d/%-2d  %2d       %.2f\n",
		report.Tied.Fired, report.Tied.Cases, report.Tied.Correct, report.Tied.Precision)
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
		if chain.Kind == "bucket" || chain.Kind == "haystack-noise" || chain.Kind == "concluded-noise" || chain.Kind == "background" {
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
		// A tied chain exists for the reworded arm alone: its store carries a
		// sentence that explains the words without settling anything, which is
		// the point of it. Counting it as a plain question would report the
		// bridge's absence as a failure of the ordinary case.
		if chain.Tied {
			if chain.Paraphrase != "" {
				pterms := prompt.Terms(chain.Paraphrase)
				report.Tied.Cases++
				if pfired, pcorrect := promptBenchProbeBlock(indexDir, scope, chain.ID, pterms); pfired {
					report.Tied.Fired++
					if pcorrect {
						report.Tied.Correct++
					} else {
						report.Tied.FalseFires++
					}
				}
			}
			continue
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
			report.Decision.Cases++
			if blockCarries(indexDir, scope, terms, chain.Fact, chain.Topic) {
				report.Decision.Fired++
				report.Decision.Correct++
			}
		case "concluded":
			arm = &report.Concluded
			arm.Cases++
			if blockCarries(indexDir, scope, terms, chain.Fact, chain.Topic) {
				arm.Fired++
				arm.Correct++
			}
			continue
		case "decision-inline":
			arm = &report.DecisionInline
			arm.Cases++
			if blockCarries(indexDir, scope, terms, chain.Fact, chain.Topic) {
				arm.Fired++
				arm.Correct++
			}
			continue
		case "decision-en":
			arm = &report.Decision
			arm.Cases++
			if blockCarries(indexDir, scope, terms, chain.Fact, chain.Topic) {
				arm.Fired++
				arm.Correct++
			}
			continue
		case "short-subject":
			arm = &report.ShortSubject
			arm.Cases++
			// Through the hook, not the copy of its loop above: measured by
			// asking both about every chain in the corpus, this is the one arm
			// of seven where they disagree — "как там pr, смержился?" scored
			// correct here while the hook answered nothing.
			if fired, carries := hookEndToEndAs(indexDir, scope, chain.Question, chain.Fact, chain.Topic,
				"benchshort"); fired {
				arm.Fired++
				if carries {
					arm.Correct++
				} else {
					arm.FalseFires++
				}
			}
			continue
		case "echo-line":
			arm = &report.Echo
			arm.Cases++
			arm.Fired++
			if blockOpensOnEcho(indexDir, scope, terms, chain.Question) {
				arm.FalseFires++
			} else {
				arm.Correct++
			}
			continue
		case "tool-only":
			arm = &report.ToolOnly
			arm.Cases++
			if fired, _ := hookEndToEndAs(indexDir, scope, chain.Question, chain.Fact, chain.Topic,
				"benchtoolonly"); fired {
				arm.Fired++
				arm.FalseFires++
			} else {
				arm.Correct++
			}
			continue
		case "hook-e2e-self":
			arm = &report.EndToEnd
			arm.Cases++
			// Asked from the session that holds the answer: recalling it to
			// itself spends tokens to say what is already on screen.
			if fired, carries := hookEndToEndAs(indexDir, scope, chain.Question, chain.Fact, chain.Topic,
				chain.ID+"-session"); fired && carries {
				arm.Fired++
				arm.FalseFires++
			} else {
				arm.Correct++
			}
			continue
		case "hook-e2e":
			arm = &report.EndToEnd
			arm.Cases++
			fired, carries := hookEndToEnd(indexDir, scope, chain.Question, chain.Fact, chain.Topic)
			if fired {
				arm.Fired++
				if carries {
					arm.Correct++
				} else {
					arm.FalseFires++
				}
			}
			continue
		case "compound-subject":
			arm = &report.Compound
			arm.Cases++
			if fired, correct := promptBenchProbe(indexDir, scope, chain.ID, terms); fired {
				arm.Fired++
				if correct {
					arm.Correct++
				} else {
					arm.FalseFires++
				}
			}
			continue
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
		if chain.Paraphrase != "" {
			pterms := prompt.Terms(chain.Paraphrase)
			arm := &report.Paraphrase
			arm.Cases++
			if pfired, pcorrect := promptBenchProbe(indexDir, scope, chain.ID, pterms); pfired {
				arm.Fired++
				if pcorrect {
					arm.Correct++
				} else {
					arm.FalseFires++
				}
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
			// Background sessions are the corpus's working noise, built from
			// the same vocabulary the controls are: asking one of them inside
			// its own project matches its own text, which measures the fixture
			// rather than the bar.
			if chain.Kind == "background" {
				continue
			}
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
	finishPromptArm(&report.Paraphrase, nil)
	finishPromptArm(&report.Tied, nil)
	for _, q := range absentSubjectQuestions() {
		report.AbsentSubject.Cases++
		if fired, _ := promptBenchProbe(indexDir, bench.PromptHaystackProject, "no-such-chain", prompt.Terms(q)); fired {
			report.AbsentSubject.Fired++
			report.AbsentSubject.FalseFires++
		}
	}
	finishPromptArm(&report.Shown, nil)
	finishPromptArm(&report.Decoy, nil)
	finishPromptArm(&report.Decision, nil)
	finishPromptArm(&report.DecisionInline, nil)
	finishPromptArm(&report.Concluded, nil)
	finishPromptArm(&report.ShortSubject, nil)
	finishPromptArm(&report.Echo, nil)
	finishPromptArm(&report.Compound, nil)
	finishPromptArm(&report.EndToEnd, nil)
	finishPromptArm(&report.ToolOnly, nil)
	finishPromptArm(&report.AbsentSubject, nil)
	return report, nil
}

// shownLineCarriesATerm builds the block the hook would inject and reports
// whether its first content line holds any of the query's terms. A block whose
// opening line came from the top of a long transcript does not, and that line
// is the whole frame an agent reads before deciding to ignore the rest.
func shownLineCarriesATerm(dir, project string, terms []string) bool {
	ranked, matched, strong, idfOf, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil {
		return false
	}
	var keep []model.Session
	for i, s := range ranked {
		if !search.RecallWorthShowing(terms, matched[i], strong[i], idfOf) {
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
		if !search.RecallWorthShowing(terms, matched[i], strong[i], idfOf) {
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

// blockCarries builds the block the hook would inject and reports whether it
// holds a distinctive word of what the session concluded.
func blockCarries(dir, project string, terms []string, fact, topic string) bool {
	ranked, matched, strong, idfOf, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil || len(ranked) == 0 {
		return false
	}
	terms = byIdentifying(terms, idfOf)
	var keep []model.Session
	for i := range ranked {
		if !search.RecallWorthShowing(terms, matched[i], strong[i], idfOf) {
			continue
		}
		keep = append(keep, ranked[i])
		if len(keep) == 2 {
			break
		}
	}
	if len(keep) == 0 {
		return false
	}
	// The same ordering the hook applies, from the same function.
	keep = search.LeadWithConclusion(keep, terms)
	block := search.AutoRecallDigestFor(keep[:1], 2000, terms)
	// Skip the subject itself. It appears in every session that mentions the
	// thing, so checking for it asks whether the block is about the subject —
	// which it is either way — rather than whether it carries the conclusion.
	// An arm that did not skip it read 2/2 even after the conclusion had been
	// emptied of its content.
	for _, w := range strings.Fields(fact) {
		if len([]rune(w)) < 7 || search.TextCarriesTerm(topic, w) {
			continue
		}
		if search.TextCarriesTerm(block, w) {
			return true
		}
	}
	return false
}

// promptBenchProbe runs the same two steps the hook runs: extract terms, then
// ask the index for this project's sessions ranked by them. Anything the hook
// would discard downstream is discarded here too, so the number reported is
// what a user would actually see.

// promptBenchProbeBlock asks what the block would carry rather than what leads
// it. The block has two slots, and for a question asked in other words the
// first is often the session that explains the vocabulary — "the quorum read is
// the one that asks every replica" — which is a better lexical match than any
// answer can be, because it holds both vocabularies. Demanding it be beaten
// measures the fixture; asking whether the answer travels with it measures the
// search.
func promptBenchProbeBlock(dir, project, chainID string, terms []string) (fired, correct bool) {
	if !promptTermsWorthAsking(terms) {
		return false, false
	}
	ranked, matched, strong, idfOf, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil {
		return false, false
	}
	shown := 0
	var chosen []model.Session
	for i, s := range ranked {
		if !search.RecallWorthShowing(terms, matched[i], strong[i], idfOf) {
			continue
		}
		if len(s.Messages) > dejaVuMaxMessages {
			if s = focusSession(s, terms); len(s.Messages) == 0 {
				continue
			}
		}
		// The same dedup the hook applies: two slots are two answers, and a
		// store that said one thing in three sessions must not fill the block
		// with it (#2328).
		if sameAnswerAs(chosen, s, terms) {
			continue
		}
		chosen = append(chosen, s)
		fired = true
		if strings.HasPrefix(s.ID, chainID) {
			correct = true
		}
		shown++
		if shown == promptBlockSlots {
			break
		}
	}
	return fired, correct
}

// promptBlockSlots is how many sessions the per-prompt block carries.
const promptBlockSlots = 2

func promptBenchProbe(dir, project, chainID string, terms []string) (fired, correct bool) {
	if !promptTermsWorthAsking(terms) {
		return false, false
	}
	ranked, matched, strong, idfOf, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil {
		return false, false
	}
	for i, s := range ranked {
		// The same bar the hook applies, from the same function — kept in one
		// place because the two drifted: this one asked whether the query held
		// an identifier, the hook asked whether the session did.
		if !search.RecallWorthShowing(terms, matched[i], strong[i], idfOf) {
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

// blockOpensOnEcho reports whether the first line the agent would read is the
// question it just asked, handed back.
func blockOpensOnEcho(dir, project string, terms []string, question string) bool {
	ranked, matched, strong, idfOf, err := index.ProjectRelevant(dir, []string{project}, terms, 8)
	if err != nil {
		return false
	}
	var keep []model.Session
	for i, s := range ranked {
		if !search.RecallWorthShowing(terms, matched[i], strong[i], idfOf) {
			continue
		}
		keep = append(keep, s)
		if len(keep) == 2 {
			break
		}
	}
	if len(keep) == 0 {
		return false
	}
	for _, ln := range strings.Split(search.AutoRecallDigestForAsked(keep, 2000, terms, question), "\n") {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "- User:") && !strings.HasPrefix(t, "- Assistant:") {
			continue
		}
		return strings.Contains(strings.ToLower(t), strings.ToLower(question))
	}
	return false
}

// hookEndToEnd runs the real hook over the bench index and reports whether it
// spoke and whether what it showed carries the fact. The probe above copies the
// hook's loop; this calls it.
func hookEndToEnd(dir, project, question, fact, topic string) (fired, carries bool) {
	return hookEndToEndAs(dir, project, question, fact, topic, "benche2e")
}

func hookEndToEndAs(dir, project, question, fact, topic, sid string) (fired, carries bool) {
	for _, suf := range []string{".hookseen", ".dejavu", ".envblock", ".injections.jsonl", ".usage.jsonl"} {
		_ = os.Remove(dir + suf)
	}
	old := os.Getenv("CLAUDE_PROJECT_DIR")
	if err := os.Setenv("CLAUDE_PROJECT_DIR", "/"+project); err != nil {
		return false, false
	}
	defer func() { _ = os.Setenv("CLAUDE_PROJECT_DIR", old) }()

	var out bytes.Buffer
	payload := fmt.Sprintf(`{"prompt":%q,"session_id":%q}`, question, sid)
	if err := runHookPromptMode(dir, strings.NewReader(payload), &out, true); err != nil {
		return false, false
	}
	got := out.String()
	if !strings.Contains(got, "deja-recall") {
		return false, false
	}
	// Seven runes was a guard against matching the subject itself, which every
	// block says. It also made a fact of ordinary short words unmatchable: "the
	// pr went in after the flake was fixed" has no word that long, so the arm
	// scored a false fire no product change could ever fix. Skip the short
	// words and the subject instead of the short words alone.
	for _, w := range strings.Fields(fact) {
		w = strings.Trim(w, ".,:;!?()\"'\u00ab\u00bb")
		if len([]rune(w)) < 5 || strings.EqualFold(w, topic) {
			continue
		}
		if strings.Contains(strings.ToLower(got), strings.ToLower(w)) {
			return true, true
		}
	}
	return true, false
}
