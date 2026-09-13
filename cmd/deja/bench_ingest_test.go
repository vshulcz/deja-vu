package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/bench"
)

// The gate this benchmark exists to be: an update that only added bytes must
// not rewrite the records already on file. That is what the new-transcript path
// got wrong for two months (#3500), and wall time on a shared runner is too
// noisy to catch it — the bytes are not.
func TestBenchIngestOnlyARewriteRewritesTheStore(t *testing.T) {
	hermeticEnv(t)
	report, err := measureIngest(bench.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Classes) != 5 {
		t.Fatalf("measured %d update classes, want the five a store sees: %#v", len(report.Classes), report.Classes)
	}
	for _, c := range report.Classes {
		rewriteExpected := strings.HasPrefix(c.Name, "rewritten")
		if c.Rewritten != rewriteExpected {
			t.Errorf("%q rewrote the store = %v, want %v", c.Name, c.Rewritten, rewriteExpected)
		}
		if c.RecordsMB <= 0 {
			t.Errorf("%q left no records behind, so the pass measured nothing", c.Name)
		}
	}
	if added := report.Classes[2].AddedKB; added <= 0 {
		t.Errorf("a new transcript added %.2f KB of records, so it was not indexed", added)
	}
	// A rename adds nothing: the bytes are already on file under the old name,
	// and re-reading them was what #3546 fixed.
	renamed := report.Classes[3]
	if renamed.Name != "renamed transcript" {
		t.Fatalf("class 3 is %q, want the rename", renamed.Name)
	}
	if renamed.AddedKB != 0 {
		t.Errorf("a rename added %.2f KB of records, so the file was read again", renamed.AddedKB)
	}
}
