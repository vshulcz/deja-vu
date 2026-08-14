package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A word the corpus is full of gains nothing from its neighbours: it is not a
// typo, and widening it only adds postings that discriminate nothing. This is
// what keeps an ordinary question off the candidate walk. A rare word is still
// widened even when spelled correctly, because there a near-neighbour is
// usually the same thing said differently — measured: narrowing those too cost
// longmemeval hit@5 2.5 points.
func TestFuzzyWidensRareWordsAndLeavesCommonOnesAlone(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// "deploy" lands well past the common bar; "rice" and "ice" stay rare and
	// one edit apart. The bar counts postings, which are per message, so one
	// busy session carries it — a file per posting made this the slowest test
	// in the package on Windows.
	var busy strings.Builder
	for i := range commonTokenPostings + 20 {
		fmt.Fprintf(&busy, "we deploy the service again, run %d\n", i)
	}
	writeMessages(t, proj, "common", busy.String())
	writeSession(t, proj, "rice", "jasmine rice is the one I keep buying")
	writeSession(t, proj, "ice", "the ice machine in the hotel was broken")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	_, variants, err := fuzzyPostings(dir, []string{"deploy"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := variants["deploy"]; len(v) != 1 || v[0] != "deploy" {
		t.Fatalf("a word the corpus is full of was widened to %v", v)
	}

	_, variants, err = fuzzyPostings(dir, []string{"rice"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(variants["rice"]) < 2 {
		t.Fatalf("a rare word lost its neighbours: %v", variants["rice"])
	}

	// A real typo still has to reach the word behind it.
	_, variants, err = fuzzyPostings(dir, []string{"rcie"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range variants["rcie"] {
		if v == "rice" {
			found = true
		}
	}
	if !found {
		t.Fatalf("typo no longer reaches the word it meant: %v", variants["rcie"])
	}
}
