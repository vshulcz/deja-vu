package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	full := read("longmemeval-s-full.json")
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
		{"the full-set questions", fmt.Sprintf("(%d questions, abstention included)", full.Total.N)},
		{"the full-set hit@1", "hit@1 " + pct1(full.Total.Hit1)},
		{"the full-set MRR", "MRR " + mrr3(full.Total.MRR)},
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

// The headline figure is quoted in six other places, and only the guide page
// was pinned to the run. Nothing said which run: the committed record is the
// 470-question cleaned set, `-skip-abs`, and a re-run without that flag returns
// 84.8% — which reads as drift in every one of these files and cost an hour of
// chasing a number that had not moved. The guide page states both denominators;
// the short quotes state one, so they have to state the one the record holds.
func TestQuotedHeadlineNumbersComeFromTheRuns(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "longmemeval-s-cleaned.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lme benchArtifact
	if err := json.Unmarshal(b, &lme); err != nil {
		t.Fatal(err)
	}
	locomoRaw, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "locomo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var locomo benchArtifact
	if err := json.Unmarshal(locomoRaw, &locomo); err != nil {
		t.Fatal(err)
	}

	fullRaw, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "longmemeval-s-full.json"))
	if err != nil {
		t.Fatal(err)
	}
	var full benchArtifact
	if err := json.Unmarshal(fullRaw, &full); err != nil {
		t.Fatal(err)
	}

	pct1 := func(v float64) string { return fmt.Sprintf("%.1f", v) }
	wantHit1 := pct1(lme.Total.Hit1)
	wantFull := pct1(full.Total.Hit1)
	wantLoCoMo := pct1(locomo.Total.Hit1)

	// In these files hit@1 is only ever ours — the comparison table quotes the
	// neighbours' own metrics under their own names (R@5, BEAM) and never a
	// hit@1. If that changes, this check needs the cell, not the file.
	quoted := regexp.MustCompile(`([0-9]+\.[0-9])%\s*hit@1`)
	for _, name := range []string{
		"README.md",
		"README.zh.md",
		"npm/README.md",
		"docs/llms.txt",
		"docs/guide/compare.html",
	} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := string(raw)
		// By offset, not by the matched text: two quotes in one file can read
		// the same and belong to different runs, and searching for the string
		// finds the first one every time.
		found := quoted.FindAllStringSubmatchIndex(text, -1)
		if len(found) == 0 {
			t.Errorf("%s no longer quotes hit@1; if that is on purpose, take it out of this list", name)
		}
		for _, m := range found {
			whole, got := text[m[0]:m[1]], text[m[2]:m[3]]
			// A quote that names the full set is about the other run. The
			// comparison table does that on purpose, because the neighbour's
			// cell states its own 500-question denominator and a row where
			// only one side says what it counted is not a comparison.
			want := wantHit1
			if namesTheFullSet(text, m[0], m[1], full.Total.N) {
				want = wantFull
			}
			if got != want {
				t.Errorf("%s says %q; the run it names scored %s%%", name, whole, want)
			}
		}
		// Both READMEs put the LoCoMo figure next to it, in a sentence that
		// names no metric.
		if strings.Contains(text, "LoCoMo") && strings.Contains(name, "README") {
			if !strings.Contains(text, wantLoCoMo+"%") {
				t.Errorf("%s does not carry the LoCoMo run's %s%%", name, wantLoCoMo)
			}
		}
	}

	// The landing page animates the figure, so it lives in an attribute no
	// text search reaches. The label under the counter is what identifies it.
	index, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := countedBefore(string(index), "hit@1 · LongMemEval-S")
	if !ok {
		t.Fatal("docs/index.html has no counter labelled hit@1 · LongMemEval-S")
	}
	if got != wantHit1 {
		t.Errorf("the landing counter is %s; the committed run is %s", got, wantHit1)
	}
}

// namesTheFullSet reports whether a quoted figure says, within the sentence
// around it, that it counted every question rather than the cleaned set.
func namesTheFullSet(text string, start, end, n int) bool {
	// A narrow window on purpose: one cell states both runs, so a wide one
	// reads the full set's denominator as the cleaned figure's too.
	lo := start - 40
	if lo < 0 {
		lo = 0
	}
	hi := end + 40
	if hi > len(text) {
		hi = len(text)
	}
	return strings.Contains(text[lo:hi], fmt.Sprint(n))
}

// countedBefore returns the data-count value of the counter a label belongs to.
func countedBefore(html, label string) (string, bool) {
	at := strings.Index(html, label)
	if at < 0 {
		return "", false
	}
	const attr = `data-count="`
	open := strings.LastIndex(html[:at], attr)
	if open < 0 {
		return "", false
	}
	rest := html[open+len(attr):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// withThousands writes 1982 the way the page does.
func withThousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
