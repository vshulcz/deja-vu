package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The benchmarks page and the day-zero page publish hit@1 for the same dataset
// four and a half times apart — per-question haystacks against all 19,195
// sessions at once — and each now names the other's figure so the gap reads as
// a setup and not a contradiction (#3851). Those quotes are copies, so they are
// checked against the numbers they copy.
func TestDayZeroAndBenchmarksQuoteEachOther(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	bench, day := read("benchmarks.html"), read("day-zero.html")

	lme := regexp.MustCompile(`<b>LongMemEval-S</b> \(session-level retrieval[^)]*\): <b>hit@1 ([\d.]+)%</b>`).FindStringSubmatch(bench)
	if lme == nil {
		t.Fatal("benchmarks.html no longer states LongMemEval-S hit@1")
	}
	if !regexp.MustCompile(`deja's hit@1 is ` + regexp.QuoteMeta(lme[1]) + `%`).MatchString(day) {
		t.Errorf("day-zero.html should quote the benchmark's hit@1 of %s%%", lme[1])
	}

	row := regexp.MustCompile(`<tr><td>hit@1 / 100</td><td class="us y">(\d+)</td>`).FindStringSubmatch(day)
	if row == nil {
		t.Fatal("day-zero.html no longer has deja's hit@1 cell")
	}
	if !regexp.MustCompile(`lands at ` + row[1] + `% hit@1`).MatchString(bench) {
		t.Errorf("benchmarks.html should quote day zero's hit@1 of %s%%", row[1])
	}

	// The corpus is LongMemEval's assistant chats in coding-agent file layouts,
	// not coding work (#3860).
	if regexp.MustCompile(`coding-agent sessions`).MatchString(day) {
		t.Error(`day-zero.html calls its corpus "coding-agent sessions"; it is general chat in coding-agent file layouts`)
	}
}
