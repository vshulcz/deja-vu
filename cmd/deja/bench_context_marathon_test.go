package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/vshulcz/deja-vu/internal/bench"
)

// Every corpus in this benchmark files history the way no real store does:
// many short sessions, one durable fact each. On this machine's own store a
// recall page's deepest hit matched between 24 and 23,278 times, and a hit may
// quote three of its matches — so the whole class of "the answer is inside one
// long session" was outside what `deja bench` could see. The marathon corpus
// is the same chains with the same facts, folded into one session each, and it
// shows what that costs the session-start block.
func TestTheContextBenchMeasuresOneLongSessionToo(t *testing.T) {
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
	if report.MarathonChains != bench.ContextMarathonCount {
		t.Errorf("marathon chains: %d, want %d", report.MarathonChains, bench.ContextMarathonCount)
	}
	if report.MarathonHash == "" || report.MarathonHash == report.CorpusHash {
		t.Errorf("the folded corpus is not a corpus of its own: %q against %q", report.MarathonHash, report.CorpusHash)
	}
	for _, arm := range []string{"deja-recall", "deja-digest", "deja-block", "full-history", "naive-grep", "cold"} {
		if _, ok := report.MarathonArms[arm]; !ok {
			t.Fatalf("missing marathon arm %q", arm)
		}
	}
	// The property the corpus exists to show: the same facts in one session
	// reach the agent less often than the same facts in fourteen. If this
	// stops holding, either the block got better at long sessions — worth
	// knowing — or the fold stopped folding.
	normal := report.Arms["deja-block"].MedianCoverage
	folded := report.MarathonArms["deja-block"].MedianCoverage
	if folded >= normal {
		t.Errorf("the block reaches %.2f of the facts in one long session against %.2f in fourteen short ones — the corpora no longer differ", folded, normal)
	}
	if folded <= 0 {
		t.Errorf("the block reaches nothing at all on the folded corpus, so the column cannot move: %.2f", folded)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark wrote outside scratch: %v", entries)
	}
}

// The fold keeps the content and changes only the filing.
func TestFoldingAChainKeepsEveryMessage(t *testing.T) {
	normal := bench.GenerateContext(7)
	folded := bench.GenerateContextMarathon(7)
	if len(folded.Chains) != bench.ContextMarathonCount {
		t.Fatalf("folded chains: %d", len(folded.Chains))
	}
	for i, fc := range folded.Chains {
		nc := normal.Chains[i]
		if fc.ID != nc.ID {
			t.Fatalf("chain %d is not the same chain: %q against %q", i, fc.ID, nc.ID)
		}
		if len(fc.Sessions) != 2 {
			t.Fatalf("%s folded into %d sessions, want the marathon and the task", fc.ID, len(fc.Sessions))
		}
		want, got := 0, len(fc.Sessions[0].Messages)
		for _, s := range nc.Sessions[:len(nc.Sessions)-1] {
			want += len(s.Messages)
		}
		if got != want {
			t.Errorf("%s: folded session holds %d messages, the chain had %d", fc.ID, got, want)
		}
		if fc.Sessions[1].ID != nc.ID+"-task" {
			t.Errorf("%s: the task session was folded in too: %q", fc.ID, fc.Sessions[1].ID)
		}
	}
}
