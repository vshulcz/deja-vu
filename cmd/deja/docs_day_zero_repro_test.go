package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The day-zero page hands the reader one command and says it produces the deja
// row. It did not: without -corpus the pile is the haystacks of the scored
// questions alone — 4,604 sessions, not the 19,195 the page is about, and every
// number in the column moves (#3847). The page and the flag have to agree.
func TestTheDayZeroCommandLaysDownTheCorpusThePageReports(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "day-zero.html"))
	if err != nil {
		t.Fatal(err)
	}

	cmd := regexp.MustCompile(`go run \./scripts/day0bench[^<]*`).Find(page)
	if cmd == nil {
		t.Fatal("the page no longer shows a day0bench command")
	}
	for _, flag := range []string{"-data ", "-limit 100", "-corpus 500", "-keep "} {
		if !strings.Contains(string(cmd), flag) {
			t.Errorf("the documented command is missing %q: %s", flag, cmd)
		}
	}

	// LongMemEval-S is 500 questions, so -corpus 500 is the whole set and the
	// only value that reaches the session count the page states everywhere.
	if !strings.Contains(string(page), "19,195 sessions") {
		t.Error("the page no longer says 19,195 sessions; the -corpus value above needs rechecking")
	}
}
