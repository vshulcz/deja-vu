package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestBenchRecallJSONAndIsolation(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "recall", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		CorpusHash string `json:"corpus_hash"`
		Sessions   int    `json:"sessions"`
		Queries    int    `json:"queries"`
		Lexical    struct {
			RecallAt5  float64 `json:"recall_at_5"`
			RecallAt10 float64 `json:"recall_at_10"`
			MedianMS   float64 `json:"median_latency_ms"`
		} `json:"lexical"`
		HybridStatus string `json:"hybrid_status"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid benchmark JSON %q: %v", out, err)
	}
	if len(report.CorpusHash) != 64 || report.Sessions != 500 || report.Queries != 50 || report.Lexical.RecallAt5 < 0.85 || report.Lexical.RecallAt10 < report.Lexical.RecallAt5 || report.Lexical.MedianMS < 0 || report.HybridStatus == "" {
		t.Fatalf("unexpected benchmark report: %#v", report)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark wrote outside its temp directory: %v", entries)
	}
}

func TestBenchRecallHumanAndInvalidArgs(t *testing.T) {
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "recall")
	if err != nil || !strings.Contains(out, "recall@5") || !strings.Contains(out, "hybrid: endpoint unavailable, skipped") {
		t.Fatalf("human benchmark output=%q err=%v", out, err)
	}
	if _, err := captureRun(t, "bench", "other"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal("invalid benchmark command did not fail")
	}
	if _, err := captureRun(t, "bench", "recall", "--bad"); err == nil {
		t.Fatal("invalid benchmark flag did not fail")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "index.db")); !os.IsNotExist(err) {
		t.Fatalf("unexpected real index path state: %v", err)
	}
}

func TestBenchHelpers(t *testing.T) {
	hits := []search.Hit{{Session: model.Session{ID: "one"}}, {Session: model.Session{ID: "two"}}}
	if !containsRelevant(hits, []string{"one"}, 1) || containsRelevant(hits, []string{"two"}, 1) {
		t.Fatal("relevance cutoff is wrong")
	}
	var out bytes.Buffer
	printBenchReport(&out, benchReport{Sessions: 1, Queries: 1, Lexical: benchMetric{RecallAt5: 1}, Hybrid: &benchMetric{RecallAt10: 1}, HybridStatus: "available"})
	if !strings.Contains(out.String(), "hybrid") {
		t.Fatalf("hybrid report missing: %q", out.String())
	}
	if _, err := measureRecall(filepath.Join(t.TempDir(), "missing"), []bench.Query{{Text: "missing"}}, nil); err == nil {
		t.Fatal("missing benchmark index did not fail")
	}
	root := t.TempDir()
	when := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	session := model.Session{ID: "helper", Project: "project", Messages: []model.Message{{Role: "user", Text: "helper", Time: when}}}
	if err := writeBenchCorpus(root, []model.Session{session}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "project", "helper.jsonl")); err != nil {
		t.Fatal(err)
	}
	badRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBenchCorpus(badRoot, []model.Session{session}); err == nil {
		t.Fatal("writing under a file did not fail")
	}
}

func TestBenchmarkTempDirStaysInWorkspace(t *testing.T) {
	dir, err := benchmarkTempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(workingDir, dir)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("benchmark temp dir escaped workspace: %q", dir)
	}
}

func TestBenchmarkTempDirRejectsSquattedParent(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(workingDir) }()
	if err := os.WriteFile(filepath.Join(root, ".deja-bench"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := benchmarkTempDir(); err == nil {
		t.Fatal("benchmark accepted a squatted scratch path")
	}
	if err := runBenchRecall(false); err == nil {
		t.Fatal("benchmark ignored a squatted scratch path")
	}
}

func TestMeasureRecallUsesFuzzyVariants(t *testing.T) {
	root := t.TempDir()
	corpus := bench.Generate(bench.Seed)
	claudeRoot := filepath.Join(root, "claude")
	if err := writeBenchCorpus(claudeRoot, corpus.Sessions); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(root, "index.db")
	restore := isolateBenchEnv(root, claudeRoot, indexDir)
	defer restore()
	if err := index.EnsureForSearch(indexDir, search.Options{Query: "", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := measureRecall(indexDir, []bench.Query{{Text: "invaldtion", Relevant: []string{"session-000"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := measureRecall(indexDir, []bench.Query{{Text: "cache", Relevant: []string{"session-000"}}}, &embed.Client{}); err == nil {
		t.Fatal("missing hybrid sidecar did not fail")
	}
}

func TestBenchHybridWhenEndpointIsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("embedding request: %v", err)
			return
		}
		vectors := make([][]float32, len(request.Input))
		for i := range vectors {
			vectors[i] = []float32{1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	defer server.Close()
	t.Setenv("DEJA_EMBED_URL", server.URL)
	if _, err := captureRun(t, "bench", "recall", "--json"); err != nil {
		t.Fatal(err)
	}
}
