package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The published numbers used to be re-runnable and not checkable: the harnesses
// were committed and the runs were not, so a reader either took the docs on
// faith or re-ran the benchmark themselves. The independent review at
// neoneye/agent-memory-atlas said exactly that.
//
// docs/benchmarks/ now holds what each run wrote, and this pins the guide page
// to those files: every headline figure on the page has to be the figure in the
// artifact, rounded the way the page rounds it. A re-run that moves a number
// moves the page or fails here.
type benchArtifact struct {
	Benchmark string `json:"benchmark"`
	Questions int    `json:"questions"`
	Dataset   struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	} `json:"dataset"`
	Rows  []benchRow `json:"rows"`
	Total benchRow   `json:"total"`
	Extra struct {
		EvidenceRecall map[string]float64 `json:"evidence_recall"`
	} `json:"extra"`
}

type benchRow struct {
	Name  string   `json:"name"`
	N     int      `json:"n"`
	Hit1  float64  `json:"hit@1"`
	Hit5  float64  `json:"hit@5"`
	Hit10 *float64 `json:"hit@10"`
	Hit20 *float64 `json:"hit@20"`
	MRR   float64  `json:"mrr"`
}

func TestBenchmarkPageMatchesTheCommittedRuns(t *testing.T) {
	root := filepath.Join("..", "..")
	page, err := os.ReadFile(filepath.Join(root, "docs", "guide", "benchmarks.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)

	read := func(name string) benchArtifact {
		b, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", name))
		if err != nil {
			t.Fatalf("%s: %v — the page cites a run whose record is missing", name, err)
		}
		var a benchArtifact
		if err := json.Unmarshal(b, &a); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if a.Questions == 0 || a.Total.N == 0 || a.Dataset.SHA256 == "" {
			t.Fatalf("%s: a record without questions, a total or a dataset digest", name)
		}
		if len(a.Rows) < 2 {
			t.Fatalf("%s: %d slices, so the record is a total with nothing behind it", name, len(a.Rows))
		}
		return a
	}

	lme := read("longmemeval-s-cleaned.json")
	locomo := read("locomo.json")

	// One decimal is how the page writes a percentage, and three how it writes
	// an MRR; anything the page states has to survive that rounding.
	pct1 := func(v float64) string { return fmt.Sprintf("%.1f%%", v) }
	mrr3 := func(v float64) string { return fmt.Sprintf("%.3f", v) }

	for _, want := range []struct{ what, text string }{
		{"LongMemEval questions", fmt.Sprintf("%d questions on the cleaned set", lme.Questions)},
		{"LongMemEval hit@1", "hit@1 " + pct1(lme.Total.Hit1)},
		{"LongMemEval hit@5", "hit@5 " + pct1(lme.Total.Hit5)},
		{"LongMemEval MRR", "MRR " + mrr3(lme.Total.MRR)},
		{"LoCoMo questions", fmt.Sprintf("%s questions", withThousands(locomo.Total.N))},
		{"LoCoMo hit@1", "hit@1 " + pct1(locomo.Total.Hit1)},
		{"LoCoMo hit@5", "hit@5 " + pct1(locomo.Total.Hit5)},
		{"LoCoMo MRR", "MRR " + mrr3(locomo.Total.MRR)},
	} {
		if !strings.Contains(html, want.text) {
			t.Errorf("benchmarks.html does not state %s as the run recorded it: %q",
				want.what, want.text)
		}
	}

	if lme.Total.Hit10 == nil || lme.Total.Hit20 == nil {
		t.Fatal("the LongMemEval record has no hit@10 or hit@20")
	}
	for _, want := range []string{
		"hit@10 " + pct1(*lme.Total.Hit10),
		"hit@20 " + pct1(*lme.Total.Hit20),
	} {
		if !strings.Contains(html, want) {
			t.Errorf("benchmarks.html does not state %q", want)
		}
	}

	// The weakest slice is named on the page with its own number, and it is the
	// one a reader checks first.
	for _, r := range lme.Rows {
		if r.Name == "single-session-preference" {
			if !strings.Contains(html, pct1(r.Hit1)+" hit@1") {
				t.Errorf("the preference slice is %s in the run and the page does not say so", pct1(r.Hit1))
			}
		}
	}

	for _, want := range []struct{ at, text string }{
		{"@1", "evidence-recall@1 " + pct1(lme.Extra.EvidenceRecall["@1"])},
		{"@5", "@5 " + pct1(lme.Extra.EvidenceRecall["@5"])},
	} {
		if !strings.Contains(html, want.text) {
			t.Errorf("benchmarks.html does not state evidence recall %s as recorded: %q", want.at, want.text)
		}
	}
}

// withThousands writes 1982 the way the page does.
func withThousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
