package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

type benchMetric struct {
	RecallAt5             float64 `json:"recall_at_5"`
	RecallAt10            float64 `json:"recall_at_10"`
	MedianMS              float64 `json:"median_latency_ms"`
	SemanticOnlyRephrased float64 `json:"semantic_only_rephrased_recall,omitempty"`
}

type benchReport struct {
	CorpusHash   string       `json:"corpus_hash"`
	Sessions     int          `json:"sessions"`
	Queries      int          `json:"queries"`
	Lexical      benchMetric  `json:"lexical"`
	Hybrid       *benchMetric `json:"hybrid,omitempty"`
	HybridStatus string       `json:"hybrid_status"`
}

func runBench(args []string) error {
	if len(args) > 0 && args[0] == "prompt" {
		return runBenchPrompt(args[1:])
	}
	if len(args) > 0 && args[0] == "context" {
		return runBenchContext(args[1:])
	}
	if len(args) > 0 && args[0] == "block" {
		return runBenchBlock(args[1:])
	}
	if len(args) < 1 || args[0] != "recall" {
		return fmt.Errorf("bench: usage: bench recall|context|prompt|block [--json] [--seed N]")
	}
	jsonOutput, seed, err := parseBenchArgs("recall", args[1:])
	if err != nil {
		return err
	}
	return runBenchRecall(jsonOutput, seed)
}

func runBenchRecall(jsonOutput bool, seed int64) error {
	corpus := bench.Generate(seed)
	root, err := benchmarkTempDir()
	if err != nil {
		return err
	}
	defer func() { releaseBenchTempDir(root) }()
	indexDir := filepath.Join(root, "index.db")
	claudeRoot := filepath.Join(root, "claude")
	if err := writeBenchCorpus(claudeRoot, corpus.Sessions); err != nil {
		return err
	}
	restore := isolateBenchEnv(root, claudeRoot, indexDir)
	defer restore()
	if err := index.EnsureForSearch(indexDir, search.Options{Query: "", All: true}, true, io.Discard); err != nil {
		return fmt.Errorf("build benchmark index: %w", err)
	}
	lexical, err := measureRecall(indexDir, corpus.Queries, nil)
	if err != nil {
		return err
	}
	report := benchReport{CorpusHash: corpus.Hash, Sessions: len(corpus.Sessions), Queries: len(corpus.Queries), Lexical: lexical, HybridStatus: "endpoint unavailable, skipped"}
	if client, probeErr := embed.New(); probeErr == nil {
		if _, embedErr := embed.EmbedIndex(indexDir, client, nil); embedErr == nil {
			var hybrid benchMetric
			hybrid, err = measureRecall(indexDir, corpus.Queries, client)
			if err != nil {
				return err
			}
			hybrid.SemanticOnlyRephrased, err = measureSemanticOnlyRephrased(indexDir, corpus.Queries, client)
			if err != nil {
				return err
			}
			report.Hybrid = &hybrid
			report.HybridStatus = "available"
		} else {
			report.HybridStatus = "endpoint unavailable, skipped"
		}
	} else {
		report.HybridStatus = "endpoint unavailable, skipped"
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	printBenchReport(os.Stdout, report)
	return nil
}

func measureSemanticOnlyRephrased(dir string, queries []bench.Query, client *embed.Client) (float64, error) {
	sidecar, err := embed.Read(dir)
	if err != nil {
		return 0, err
	}
	matched, total := 0, 0
	for i, q := range queries {
		if i%5 != 1 {
			continue
		}
		total++
		hits, searchErr := embed.SemanticSearch(context.Background(), dir, search.Options{Query: q.Text, All: true}, sidecar, client)
		if searchErr != nil {
			return 0, fmt.Errorf("semantic-only benchmark query %q: %w", q.Text, searchErr)
		}
		if containsRelevant(hits, q.Relevant, 5) {
			matched++
		}
	}
	if total == 0 {
		return 0, nil
	}
	return float64(matched) / float64(total), nil
}

func measureRecall(dir string, queries []bench.Query, client *embed.Client) (benchMetric, error) {
	latencies := make([]time.Duration, 0, len(queries))
	got5, got10 := 0, 0
	for _, q := range queries {
		started := time.Now()
		result, err := index.SearchWithRecoveryDetailed(dir, search.Options{Query: q.Text, All: true}, io.Discard)
		if err != nil {
			return benchMetric{}, fmt.Errorf("benchmark query %q: %w", q.Text, err)
		}
		o := search.Options{Query: q.Text, All: true}
		if result.Fuzzy || result.Stemmed {
			o.Fuzzy = true
			o.FuzzyVariants = result.Variants
		}
		hits, err := search.Run(result.Sessions, o)
		if err != nil {
			return benchMetric{}, fmt.Errorf("rank benchmark query %q: %w", q.Text, err)
		}
		if client != nil {
			sidecar, sidecarErr := embed.Read(dir)
			if sidecarErr != nil {
				return benchMetric{}, sidecarErr
			}
			hits, err = embed.Rerank(context.Background(), hits, q.Text, sidecar, client)
			if err != nil {
				return benchMetric{}, fmt.Errorf("hybrid benchmark query %q: %w", q.Text, err)
			}
		}
		latencies = append(latencies, time.Since(started))
		if containsRelevant(hits, q.Relevant, 5) {
			got5++
		}
		if containsRelevant(hits, q.Relevant, 10) {
			got10++
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	median := latencies[len(latencies)/2]
	return benchMetric{RecallAt5: float64(got5) / float64(len(queries)), RecallAt10: float64(got10) / float64(len(queries)), MedianMS: float64(median) / float64(time.Millisecond)}, nil
}

func containsRelevant(hits []search.Hit, ids []string, limit int) bool {
	if len(hits) > limit {
		hits = hits[:limit]
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	for _, hit := range hits {
		if wanted[hit.Session.ID] {
			return true
		}
	}
	return false
}

// benchTranscriptLine writes one message the way the harness would have
// recorded it.
//
// Tool output is not a role in a Claude transcript: it arrives as a user line
// whose content is a tool_result block, and the reader labels it tool-output
// on the way in. Writing the string "tool-output" into the type field instead
// produced a line the reader skips entirely, so the corpus could hold no tool
// output at all — a benchmark arm built on it was green because the message
// was missing, not because the behaviour was right.
func benchTranscriptLine(sid string, m model.Message) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	line := struct {
		Type    string  `json:"type"`
		ID      string  `json:"sessionId"`
		Time    string  `json:"timestamp"`
		Message message `json:"message"`
	}{Type: m.Role, ID: sid, Time: m.Time.UTC().Format(time.RFC3339),
		Message: message{Role: m.Role, Content: m.Text}}
	if m.Role == benchRoleToolOutput {
		line.Type = "user"
		line.Message = message{Role: "user", Content: []map[string]string{
			{"type": "tool_result", "content": m.Text},
		}}
	}
	b, err := json.Marshal(line)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// benchRoleToolOutput is the role a corpus chain uses to say "a tool printed
// this". It mirrors sources.RoleToolOutput.
const benchRoleToolOutput = "tool-output"

func writeBenchCorpus(root string, sessions []model.Session) error {
	for _, s := range sessions {
		path := filepath.Join(root, s.Project, s.ID+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		for _, m := range s.Messages {
			b, marshalErr := benchTranscriptLine(s.ID, m)
			if marshalErr != nil {
				_ = f.Close()
				return marshalErr
			}
			if _, writeErr := fmt.Fprintln(f, b); writeErr != nil {
				_ = f.Close()
				return writeErr
			}
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

func isolateBenchEnv(root, claudeRoot, indexDir string) func() {
	values := map[string]string{
		"HOME": root, "USERPROFILE": root, "DEJA_INDEX_DIR": indexDir,
		"DEJA_CLAUDE_ROOT": claudeRoot, "DEJA_CODEX_ROOT": filepath.Join(root, "codex"),
		"DEJA_OPENCODE_DB": filepath.Join(root, "opencode.db"), "DEJA_AIDER_ROOTS": filepath.Join(root, "aider"),
		"DEJA_GEMINI_ROOT": filepath.Join(root, "gemini"), "DEJA_CURSOR_ROOT": filepath.Join(root, "cursor"),
		"DEJA_CURSOR_CLI_ROOT": filepath.Join(root, "cursor-cli"), "DEJA_ANTIGRAVITY_ROOT": filepath.Join(root, "antigravity"),
		"DEJA_GROK_ROOT": filepath.Join(root, "grok"), "DEJA_QWEN_ROOT": filepath.Join(root, "qwen"),
		"DEJA_NOTES_FILE": filepath.Join(root, "notes.jsonl"), "CLAUDE_CONFIG_DIR": filepath.Join(root, "claude-config"),
		"CODEX_HOME": filepath.Join(root, "codex-home"), "GEMINI_CLI_HOME": filepath.Join(root, "gemini-home"),
		"CURSOR_CONFIG_DIR": filepath.Join(root, "cursor-config"), "GROK_HOME": filepath.Join(root, "grok-home"),
		"AIDER_CHAT_HISTORY_FILE": filepath.Join(root, "aider-history.md"), "XDG_CONFIG_HOME": filepath.Join(root, "config"),
		"XDG_DATA_HOME": filepath.Join(root, "data"), "APPDATA": filepath.Join(root, "appdata"),
	}
	old := make(map[string]string)
	for key, value := range values {
		old[key] = os.Getenv(key)
		_ = os.Setenv(key, value)
	}
	return func() {
		for key, value := range old {
			_ = os.Setenv(key, value)
		}
	}
}

// benchStaleRun is how old a run tree has to be before a later run reclaims it.
// A run that was interrupted leaves its corpus and index behind — megabytes of
// it — and nothing else ever looks in that directory again. Bounded by age so a
// run happening right now, in another shell, is not swept out from under it.
const benchStaleRun = 24 * time.Hour

func benchmarkTempDir() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	parent := filepath.Join(workingDir, ".deja-bench")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	sweepStaleBenchRuns(parent, time.Now())
	return os.MkdirTemp(parent, "run-")
}

// sweepStaleBenchRuns removes what interrupted runs left behind.
func sweepStaleBenchRuns(parent string, now time.Time) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "run-") {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < benchStaleRun {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, e.Name()))
	}
}

// releaseBenchTempDir drops this run's tree and, if it was the last thing in
// there, the directory bench made in the user's working directory. `deja bench`
// left an empty .deja-bench in whatever repo it was run from (#2560).
func releaseBenchTempDir(dir string) {
	_ = os.RemoveAll(dir)
	// Remove, not RemoveAll: a parent still holding another run's tree fails
	// here and stays, which is the point.
	_ = os.Remove(filepath.Dir(dir))
}

func printBenchReport(w io.Writer, report benchReport) {
	fmt.Fprintf(w, "deja bench recall\ncorpus: %d session%s, %d quer%s\n", report.Sessions, pluralS(report.Sessions), report.Queries, pluralY(report.Queries))
	fmt.Fprintln(w, "mode    recall@5  recall@10  median latency")
	fmt.Fprintf(w, "lexical %.2f      %.2f       %.2f ms\n", report.Lexical.RecallAt5, report.Lexical.RecallAt10, report.Lexical.MedianMS)
	if report.Hybrid != nil {
		fmt.Fprintf(w, "hybrid  %.2f      %.2f       %.2f ms  semantic-only rephrased %.2f\n", report.Hybrid.RecallAt5, report.Hybrid.RecallAt10, report.Hybrid.MedianMS, report.Hybrid.SemanticOnlyRephrased)
	} else {
		fmt.Fprintln(w, "hybrid: endpoint unavailable, skipped")
	}
}
