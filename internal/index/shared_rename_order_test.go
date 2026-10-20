package index

import (
	"os"
	"path/filepath"
	"testing"
)

// A renamed copy that still sorts after its owner is followed by name: a
// rebuild reads the session's files in the same order, so nothing is read
// again. Reading it took the replacing path, 20 s for 20 renames at 40,000
// sessions (#5466).
func TestARenameThatKeepsTheOrderIsNotReadAgain(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	_, b := sharedPair(t, t0, t0+claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)

	c := filepath.Join(filepath.Dir(b), "c.jsonl")
	if err := os.Rename(b, c); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if lastIngestFiles != 0 {
		t.Errorf("a copy renamed in place behind its owner read %d files again, want none", lastIngestFiles)
	}
	keptMatchesRebuild(t, dir, "s-x")

	// The control: the same copy renamed ahead of its owner takes the row,
	// and is read again for it.
	if err := os.Rename(c, filepath.Join(filepath.Dir(c), "0.jsonl")); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if lastIngestFiles == 0 {
		t.Errorf("a copy renamed ahead of its owner was followed by name alone")
	}
	keptMatchesRebuild(t, dir, "s-x")
}

func TestRenameReorders(t *testing.T) {
	for _, tc := range []struct {
		files   []string
		renamed map[string]string
		want    bool
	}{
		{[]string{"a", "c"}, map[string]string{"c": "b"}, false},
		{[]string{"0", "a"}, map[string]string{"0": "b"}, true},
		{[]string{"c", "d"}, map[string]string{"c": "a", "d": "b"}, false},
		{[]string{"c", "d"}, map[string]string{"c": "b", "d": "a"}, true},
		{[]string{"a"}, map[string]string{"a": "z"}, false},
	} {
		if got := renameReorders(tc.files, tc.renamed); got != tc.want {
			t.Errorf("renameReorders(%v, %v) = %v, want %v", tc.files, tc.renamed, got, tc.want)
		}
	}
}
