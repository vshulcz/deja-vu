package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	if len(args) < 1 || args[0] != "recall" || len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
		return fmt.Errorf("bench: usage: bench recall [--json]")
	}
	return runBenchRecall(len(args) == 2)
}

func runBenchRecall(jsonOutput bool) error {
	corpus := bench.Generate(bench.Seed)
	root, err := benchmarkTempDir()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
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
		if _, embedErr := embed.EmbedIndex(indexDir, client); embedErr == nil {
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
			line := struct {
				Type    string `json:"type"`
				ID      string `json:"sessionId"`
				Time    string `json:"timestamp"`
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			}{Type: m.Role, ID: s.ID, Time: m.Time.UTC().Format(time.RFC3339), Message: struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}{Role: m.Role, Content: m.Text}}
			b, marshalErr := json.Marshal(line)
			if marshalErr != nil {
				_ = f.Close()
				return marshalErr
			}
			if _, writeErr := fmt.Fprintln(f, string(b)); writeErr != nil {
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

func benchmarkTempDir() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	parent := filepath.Join(workingDir, ".deja-bench")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "run-")
}

func printBenchReport(w io.Writer, report benchReport) {
	fmt.Fprintf(w, "deja bench recall\ncorpus: %d sessions, %d queries\n", report.Sessions, report.Queries)
	fmt.Fprintln(w, "mode    recall@5  recall@10  median latency")
	fmt.Fprintf(w, "lexical %.2f      %.2f       %.2f ms\n", report.Lexical.RecallAt5, report.Lexical.RecallAt10, report.Lexical.MedianMS)
	if report.Hybrid != nil {
		fmt.Fprintf(w, "hybrid  %.2f      %.2f       %.2f ms  semantic-only rephrased %.2f\n", report.Hybrid.RecallAt5, report.Hybrid.RecallAt10, report.Hybrid.MedianMS, report.Hybrid.SemanticOnlyRephrased)
	} else {
		fmt.Fprintln(w, "hybrid: endpoint unavailable, skipped")
	}
}
