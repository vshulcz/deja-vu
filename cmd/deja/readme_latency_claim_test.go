package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The README states a lookup latency twice: once in the banner and once in the
// performance table, which cites where each number comes from. They disagreed —
// the banner said "sub-millisecond lookups over 5 GB of history" while the table
// says the benchmark's ~0.4 ms is on a synthetic corpus and a real haystack is
// ~25 ms. Measured on this machine's 137 MB index, an exact answer is 3.4–6.2 ms
// in process and the relevance tier 215 ms (#2608).
func TestTheReadmeBannerAgreesWithItsOwnTable(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	banner := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "hit@1") && strings.Contains(line, "lookups") {
			banner = line
			break
		}
	}
	if banner == "" {
		t.Fatal("the banner line is gone, so this measures nothing")
	}
	// The table is the sourced claim: whatever the banner says has to be
	// reachable from it.
	if !strings.Contains(text, "`deja bench recall`") {
		t.Fatal("the performance table no longer names its source")
	}
	if regexp.MustCompile(`(?i)sub-millisecond`).MatchString(banner) {
		t.Errorf("the banner promises a latency the table contradicts:\n  %s", strings.TrimSpace(banner))
	}
	// And it still says something about lookups rather than dropping the claim.
	if !strings.Contains(banner, "lookup") {
		t.Errorf("the banner stopped saying anything about lookups:\n  %s", strings.TrimSpace(banner))
	}
}
