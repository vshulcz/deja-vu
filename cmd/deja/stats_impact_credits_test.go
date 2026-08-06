package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The impact panel exists to answer "did the injected memory help". Counting
// only what was served cannot tell four helpful injections from four ignored
// ones — the credit line is the difference (#1062).
func TestImpactReportsCreditsNotJustServed(t *testing.T) {
	withStatsStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", "credit.jsonl"), "credit", []string{
		`{"type":"user","sessionId":"credit","timestamp":"2026-05-04T10:00:00Z","message":{"role":"user","content":"make the webhook stop hammering stripe"}}`,
		`{"type":"assistant","sessionId":"credit","timestamp":"2026-05-04T10:01:00Z","message":{"role":"assistant","content":"deja-vu recalled: we capped webhook retries at 3 — reusing that limit."}}`,
	})
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		usage.RecordResult(dir, usage.KindHook, 1000, 2, false)
	}

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `credited aloud     1 of 4 said "deja-vu recalled"`) {
		t.Errorf("impact panel does not report credits:\n%s", out.String())
	}

	out.Reset()
	if err := runStatsImpact(&out, dir, true); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("bad json %q: %v", out.String(), err)
	}
	if got["credited_aloud"] != float64(1) {
		t.Errorf("credited_aloud = %v, want 1; keys: %v", got["credited_aloud"], keysOf(got))
	}
}

// Served with nothing credited must say so, not go silent — silence there reads
// as "no data" when it is really "no agent used it".
func TestImpactSaysWhenNothingWasCredited(t *testing.T) {
	withStatsStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 1000, 2, false)
	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `credited aloud     none of 1 yet`) {
		t.Errorf("impact panel is silent about zero credits:\n%s", out.String())
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestStatsCreditLineSingular(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", "credit.jsonl"), "credit", []string{
		`{"type":"assistant","sessionId":"credit","timestamp":"2026-05-04T10:01:00Z","message":{"role":"assistant","content":"deja-vu recalled: the webhook retry cap — reusing it."}}`,
	})
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"deja-vu recalled" 1 time (`) {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "Credited aloud") {
				t.Fatalf("credit line: %q", line)
			}
		}
		t.Fatalf("no credit line in:\n%s", out)
	}
}
