package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A pair whose remedy is a file edit (#2163) reached `deja fix` and never the
// hook: fixLine wanted a command and gave the failing test nothing (#3259).
func TestFixLineNamesTheFileThatChangedNext(t *testing.T) {
	p := index.FixPair{
		Error:   "--- FAIL: TestLifecycleGatesTheGiveUpPenalty",
		Edit:    "internal/search/outcome_rank_test.go",
		Project: "deja-vu",
		When:    time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	got := fixLine(p, 1)
	if got == "" {
		t.Fatal("an edit remedy produced no line")
	}
	for _, want := range []string{"deja: this error came up", "in deja-vu", "changed next", "internal/search/outcome_rank_test.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("line %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "what followed it") || strings.Contains(got, " ran this ") {
		t.Errorf("an edit is offered as a command: %q", got)
	}
	p.Candidate = true
	if got := fixLine(p, 1); !strings.Contains(got, "nothing confirms") {
		t.Errorf("an unconfirmed edit reads as confirmed: %q", got)
	}
}
