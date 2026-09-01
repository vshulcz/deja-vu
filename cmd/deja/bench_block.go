package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja bench block` scores what the block says, which the other three benches
// cannot see (#2933): bench prompt prints identical columns with the block
// emptied, bench recall cannot see the order inside its own page, and bench
// context's coverage stays at 1.00 with half the arm deleted.
//
// The question here is the one #2243 asks: with the right session in hand, does
// what deja hands over carry what that session settled. The corpus knows the
// answer by construction — one sentence, in one of eight sessions that all
// discuss the subject — so a block can be scored rather than judged.
//
// Its own corpus rather than a column on bench context: the corpus has to be
// hard enough that choosing badly loses, and changing the context corpus would
// move numbers people have already compared against.

type blockArmReport struct {
	Carries      float64 `json:"carries_the_answer"`
	ReadsSettled float64 `json:"reads_as_settled"`
	MedianTokens int     `json:"median_tokens"`
}

type blockReport struct {
	CorpusHash string                    `json:"corpus_hash"`
	Seed       int64                     `json:"seed"`
	Chains     int                       `json:"chains"`
	Priors     int                       `json:"priors_per_chain"`
	Arms       map[string]blockArmReport `json:"arms"`
}

type blockMeasurement struct {
	carries  bool
	settled  bool
	tokens   int
	armEmpty bool
}

func runBenchBlock(args []string) error {
	jsonOutput, seed, err := parseBenchArgs("block", args)
	if err != nil {
		return err
	}
	report, err := measureBlock(seed)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	printBlockReport(os.Stdout, report)
	return nil
}

func measureBlock(seed int64) (blockReport, error) {
	corpus := bench.GenerateBlock(seed)
	root, err := benchmarkTempDir()
	if err != nil {
		return blockReport{}, err
	}
	defer func() { releaseBenchTempDir(root) }()
	claudeRoot := filepath.Join(root, "claude")
	indexDir := filepath.Join(root, "index.db")
	var sessions []model.Session
	for _, chain := range corpus.Chains {
		sessions = append(sessions, chain.Sessions...)
	}
	if err := writeBenchCorpus(claudeRoot, sessions); err != nil {
		return blockReport{}, err
	}
	restore := isolateBenchEnv(root, claudeRoot, indexDir)
	defer restore()
	if err := index.EnsureForSearch(indexDir, search.Options{Query: "", All: true}, true, io.Discard); err != nil {
		return blockReport{}, fmt.Errorf("build block benchmark index: %w", err)
	}

	measurements := map[string][]blockMeasurement{}
	for _, chain := range corpus.Chains {
		hits, err := blockHits(indexDir, chain)
		if err != nil {
			return blockReport{}, err
		}
		for name, text := range map[string]string{
			"deja-block":  blockArmBlock(hits, chain),
			"deja-digest": blockArmDigest(hits, chain),
			"newest-turn": blockArmNewest(hits),
			"cold":        "",
		} {
			measurements[name] = append(measurements[name], scoreBlock(text, chain))
		}
	}
	report := blockReport{
		CorpusHash: corpus.Hash, Seed: seed,
		Chains: bench.BlockChainCount, Priors: bench.BlockPriorCount,
		Arms: map[string]blockArmReport{},
	}
	for name, values := range measurements {
		report.Arms[name] = summarizeBlock(values)
	}
	return report, nil
}

// blockHits is retrieval, shared by every arm. Scoring the block means holding
// retrieval constant: an arm that answers better because it searched better
// would be measuring the thing bench recall already measures.
func blockHits(dir string, chain bench.BlockChain) ([]model.Session, error) {
	q := strings.Join(chain.Terms, " ")
	result, err := index.SearchWithRecoveryDetailed(dir, search.Options{Query: q, All: true}, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("block query %q: %w", q, err)
	}
	hits, err := search.Run(result.Sessions, search.Options{Query: q, All: true})
	if err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Session)
	}
	return out, nil
}

// blockArmBlock is the session-start block, the thing an agent is handed
// before it has asked anything.
func blockArmBlock(hits []model.Session, chain bench.BlockChain) string {
	if len(hits) == 0 {
		return ""
	}
	return search.BuildAutoRecall(hits, search.AutoRecallOptions{Mode: search.RecallAggressive}).Text
}

// blockArmDigest is the context digest, scored on its own so a change to
// either surface is attributable — the split #2931 asks for.
func blockArmDigest(hits []model.Session, chain bench.BlockChain) string {
	if len(hits) == 0 {
		return ""
	}
	// The top hit alone, which is what recall_context returns. Printing every
	// hit would carry the answer whatever the digest chose, the same way the
	// context bench's coverage column carries it whatever the arm does.
	var b bytes.Buffer
	search.PrintContext(&b, hits[0], strings.Join(chain.Terms, " "))
	return b.String()
}

// blockArmNewest is the baseline every ranking has to beat: the last two
// assistant turns of the top hit. The corpus settles its question in the
// fourth of eight sessions, so recency alone scores near zero — a bench whose
// baseline scores full marks is measuring nothing.
func blockArmNewest(hits []model.Session) string {
	if len(hits) == 0 {
		return ""
	}
	var said []string
	for _, m := range hits[0].Messages {
		if m.Role == "assistant" {
			said = append(said, m.Text)
		}
	}
	if len(said) > 2 {
		said = said[len(said)-2:]
	}
	return strings.Join(said, "\n")
}

func scoreBlock(text string, chain bench.BlockChain) blockMeasurement {
	m := blockMeasurement{tokens: len(text) / 4, armEmpty: strings.TrimSpace(text) == ""}
	if m.armEmpty {
		return m
	}
	m.carries = strings.Contains(text, chain.SettledMarker())
	m.settled = digest.CarriesDecision(text)
	return m
}

func summarizeBlock(values []blockMeasurement) blockArmReport {
	if len(values) == 0 {
		return blockArmReport{}
	}
	carries, settled := 0, 0
	tokens := make([]int, 0, len(values))
	for _, v := range values {
		if v.carries {
			carries++
		}
		if v.settled {
			settled++
		}
		tokens = append(tokens, v.tokens)
	}
	sort.Ints(tokens)
	return blockArmReport{
		Carries:      float64(carries) / float64(len(values)),
		ReadsSettled: float64(settled) / float64(len(values)),
		MedianTokens: tokens[len(tokens)/2],
	}
}

func printBlockReport(w io.Writer, report blockReport) {
	fmt.Fprintln(w, "deja bench block")
	fmt.Fprintf(w, "chains: %d, sessions discussing each: %d, settled in session %d\n",
		report.Chains, report.Priors, bench.BlockSettledAt+1)
	fmt.Fprintln(w, "arm          carries the answer  reads as settled  median tokens")
	for _, name := range []string{"deja-block", "deja-digest", "newest-turn", "cold"} {
		arm, ok := report.Arms[name]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "%-12s %-19.2f %-17.2f %d\n", name, arm.Carries, arm.ReadsSettled, arm.MedianTokens)
	}
}
