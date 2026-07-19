package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

type contextArmReport struct {
	MedianTokens   float64 `json:"median_tokens"`
	P10Tokens      float64 `json:"p10_tokens"`
	P90Tokens      float64 `json:"p90_tokens"`
	MedianCoverage float64 `json:"median_coverage"`
	NegativeMedian float64 `json:"negative_median_tokens"`
}

type contextReport struct {
	CorpusHash string                      `json:"corpus_hash"`
	Seed       int64                       `json:"seed"`
	Chains     int                         `json:"chains"`
	Negatives  int                         `json:"negative_controls"`
	Arms       map[string]contextArmReport `json:"arms"`
}

type contextMeasurement struct {
	tokens   int
	coverage float64
	negative bool
}

func runBenchContext(args []string) error {
	jsonOutput := false
	seed := bench.Seed
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--seed":
			if i+1 >= len(args) {
				return fmt.Errorf("bench context: --seed requires a number")
			}
			value, err := strconv.ParseInt(args[i+1], 10, 64)
			if err != nil {
				return fmt.Errorf("bench context: invalid seed: %w", err)
			}
			seed = value
			i++
		default:
			return fmt.Errorf("bench: usage: bench context [--json] [--seed N]")
		}
	}
	report, err := measureContext(seed)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	printContextReport(os.Stdout, report)
	return nil
}

func measureContext(seed int64) (contextReport, error) {
	corpus := bench.GenerateContext(seed)
	root, err := benchmarkTempDir()
	if err != nil {
		return contextReport{}, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	claudeRoot := filepath.Join(root, "claude")
	indexDir := filepath.Join(root, "index.db")
	var sessions []model.Session
	for _, chain := range corpus.Chains {
		sessions = append(sessions, chain.Sessions...)
	}
	if err := writeBenchCorpus(claudeRoot, sessions); err != nil {
		return contextReport{}, err
	}
	restore := isolateBenchEnv(root, claudeRoot, indexDir)
	defer restore()
	if err := index.EnsureForSearch(indexDir, search.Options{Query: "", All: true}, true, io.Discard); err != nil {
		return contextReport{}, fmt.Errorf("build context benchmark index: %w", err)
	}
	measurements := map[string][]contextMeasurement{"deja-recall": nil, "full-history": nil, "naive-grep": nil, "cold": nil}
	for _, chain := range corpus.Chains {
		deja, err := contextDeja(indexDir, chain)
		if err != nil {
			return contextReport{}, err
		}
		full := contextFullHistory(chain)
		naive, err := contextNaiveGrep(claudeRoot, chain)
		if err != nil {
			return contextReport{}, err
		}
		for name, text := range map[string]string{"deja-recall": deja, "full-history": full, "naive-grep": naive, "cold": ""} {
			measurements[name] = append(measurements[name], contextMeasurement{tokens: len(text) / 4, coverage: contextCoverage(text, chain.Facts), negative: chain.Negative})
		}
	}
	report := contextReport{CorpusHash: corpus.Hash, Seed: seed, Chains: bench.ContextChainCount, Negatives: bench.ContextNegativeCount, Arms: map[string]contextArmReport{}}
	for name, values := range measurements {
		report.Arms[name] = summarizeContext(values)
	}
	return report, nil
}

func contextDeja(dir string, chain bench.ContextChain) (string, error) {
	result, err := index.SearchWithRecoveryDetailed(dir, search.Options{Query: strings.Join(chain.Terms, " "), All: true}, io.Discard)
	if err != nil {
		return "", fmt.Errorf("context query %q: %w", chain.Task, err)
	}
	hits, err := search.Run(result.Sessions, search.Options{Query: strings.Join(chain.Terms, " "), All: true})
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	ss := make([]model.Session, 0, len(hits))
	for _, hit := range hits {
		if hit.Session.ID == chain.ID+"-task" {
			continue
		}
		ss = append(ss, hit.Session)
		search.PrintContext(&b, hit.Session, strings.Join(chain.Terms, " "))
	}
	// This is the same digest builder used by the SessionStart hook.
	b.WriteString(search.BuildAutoRecall(ss, search.AutoRecallOptions{Mode: search.RecallAggressive}).Text)
	return b.String(), nil
}

func contextFullHistory(chain bench.ContextChain) string {
	var b strings.Builder
	for _, s := range chain.Sessions[:len(chain.Sessions)-1] {
		for _, m := range s.Messages {
			fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Text)
		}
	}
	return b.String()
}

func contextNaiveGrep(root string, chain bench.ContextChain) (string, error) {
	var b strings.Builder
	for _, s := range chain.Sessions[:len(chain.Sessions)-1] {
		path := filepath.Join(root, s.Project, s.ID+".jsonl")
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		var lines []string
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		closeErr := f.Close()
		if err := scanner.Err(); err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		for i, line := range lines {
			matched := true
			for _, term := range chain.Terms {
				if !strings.Contains(strings.ToLower(line), strings.ToLower(term)) {
					matched = false
				}
			}
			if matched {
				start, end := i-1, i+2
				if start < 0 {
					start = 0
				}
				if end > len(lines) {
					end = len(lines)
				}
				for _, contextLine := range lines[start:end] {
					b.WriteString(contextLine)
					b.WriteByte('\n')
				}
			}
		}
	}
	return b.String(), nil
}

func contextCoverage(text string, facts []string) float64 {
	if len(facts) == 0 {
		return 0
	}
	covered := 0
	for _, fact := range facts {
		if strings.Contains(text, fact) {
			covered++
		}
	}
	return float64(covered) / float64(len(facts))
}

func summarizeContext(values []contextMeasurement) contextArmReport {
	tokens, coverage, negative := []int{}, []float64{}, []int{}
	for _, value := range values {
		if value.negative {
			negative = append(negative, value.tokens)
		} else {
			tokens = append(tokens, value.tokens)
			coverage = append(coverage, value.coverage)
		}
	}
	sort.Ints(tokens)
	sort.Ints(negative)
	sort.Float64s(coverage)
	return contextArmReport{MedianTokens: percentileInt(tokens, 50), P10Tokens: percentileInt(tokens, 10), P90Tokens: percentileInt(tokens, 90), MedianCoverage: percentileFloat(coverage, 50), NegativeMedian: percentileInt(negative, 50)}
}

func percentileInt(values []int, p int) float64 {
	if len(values) == 0 {
		return 0
	}
	return float64(values[(len(values)-1)*p/100])
}
func percentileFloat(values []float64, p int) float64 {
	if len(values) == 0 {
		return 0
	}
	return values[(len(values)-1)*p/100]
}

func printContextReport(w io.Writer, report contextReport) {
	fmt.Fprintf(w, "deja bench context\nchains: %d, negative controls: %d\n", report.Chains, report.Negatives)
	fmt.Fprintln(w, "arm           median tokens  p10-p90       median coverage  negative median")
	for _, name := range []string{"deja-recall", "full-history", "naive-grep", "cold"} {
		r := report.Arms[name]
		fmt.Fprintf(w, "%-13s %-14.0f %.0f-%.0f       %.2f             %.0f\n", name, r.MedianTokens, r.P10Tokens, r.P90Tokens, r.MedianCoverage, r.NegativeMedian)
	}
}
