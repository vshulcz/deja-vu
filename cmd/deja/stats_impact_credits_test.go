package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// The report and the credit count are marshalled apart and joined, because an
// embedded type's MarshalJSON answers for the whole outer struct. Joining is
// string surgery, so this pins the result: valid JSON, both keys, and no stray
// whitespace where the comma goes.
func TestImpactJSONIsWellFormedAfterTheJoin(t *testing.T) {
	var out strings.Builder
	r := usage.ImpactReport{Recalls: 3, Injections: 1, ServedBytes: 100, RawBytes: 1000, Since: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)}
	if err := printImpact(&out, r, 2, true); err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(out.String())) {
		t.Fatalf("not valid JSON:\n%s", out.String())
	}
	if strings.Contains(out.String(), "\n,") {
		t.Errorf("the join left a comma on its own line:\n%s", out.String())
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got["credited_aloud"] != float64(2) || got["since"] == nil || got["recalls"] != float64(3) {
		t.Errorf("keys lost in the join: %v", got)
	}
}
