package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/bench"
)

// The gate this benchmark exists to be: an update must not rewrite the records
// already on file. That is what the new-transcript path got wrong for two
// months (#3500), and what a rewritten transcript cost every store until
// #5387; wall time on a shared runner is too noisy to catch it — the bytes are
// not.
func TestBenchIngestNoUpdateRewritesTheStore(t *testing.T) {
	hermeticEnv(t)
	report, err := measureIngest(bench.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Classes) != 5 {
		t.Fatalf("measured %d update classes, want the five a store sees: %#v", len(report.Classes), report.Classes)
	}
	for _, c := range report.Classes {
		if c.Rewritten {
			t.Errorf("%q rewrote the store", c.Name)
		}
		if c.RecordsMB <= 0 {
			t.Errorf("%q left no records behind, so the pass measured nothing", c.Name)
		}
	}
	if added := report.Classes[2].AddedKB; added <= 0 {
		t.Errorf("a new transcript added %.2f KB of records, so it was not indexed", added)
	}
	// The records a rewrite replaced are cleared where they stand, and only
	// those: no other class clears anything (#5429).
	for i, c := range report.Classes {
		if cleared := c.ClearedKB > 0; cleared != (c.Name == "rewritten transcript") {
			t.Errorf("class %d %q cleared %.2f KB of records", i, c.Name, c.ClearedKB)
		}
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
