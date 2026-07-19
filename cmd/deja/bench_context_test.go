package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/bench"
)

func TestContextCoverageAndColdArm(t *testing.T) {
	chain := bench.GenerateContext(bench.Seed).Chains[0]
	text := strings.Join(chain.Facts, "\n")
	if contextCoverage(text, chain.Facts) != 1 {
		t.Fatal("ground-truth facts were not fully covered")
	}
	if contextCoverage("", chain.Facts) != 0 {
		t.Fatal("empty context was not zero coverage")
	}
	report := summarizeContext([]contextMeasurement{{tokens: 10, coverage: 1}, {tokens: 20, coverage: 0}, {negative: true, tokens: 5}})
	if report.MedianTokens != 10 || report.NegativeMedian != 5 {
		t.Fatalf("summary = %#v", report)
	}
}

func TestContextJSONAndIsolation(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "context", "--json", "--seed", "7")
	if err != nil {
		t.Fatal(err)
	}
	var report contextReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid context JSON %q: %v", out, err)
	}
	if report.Chains != bench.ContextChainCount || report.Negatives != bench.ContextNegativeCount || len(report.CorpusHash) != 64 {
		t.Fatalf("unexpected context report: %#v", report)
	}
	for _, arm := range []string{"deja-recall", "full-history", "naive-grep", "cold"} {
		if _, ok := report.Arms[arm]; !ok {
			t.Fatalf("missing arm %q", arm)
		}
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark wrote outside scratch: %v", entries)
	}
}

func TestContextArgs(t *testing.T) {
	if _, err := captureRun(t, "bench", "context", "--bad"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal("invalid context flag did not fail")
	}
	if _, err := captureRun(t, "bench", "context", "--seed"); err == nil {
		t.Fatal("missing seed did not fail")
	}
}
