package index

import (
	"fmt"
	"testing"
)

// A word in fifteen sessions of a hundred is what a subject looks like on a real
// store: not unique, not filler. The informativeness floor has to count it.
//
// It was set at 2.0 against a corpus where document frequency runs over whole
// sessions — and eleven marathon sessions holding 99% of everything ever said
// squash the whole scale, so a word like this scores 1.84 and was thrown away.
// Measured over 129 live prompts, the old floor answered 112 of them with 89%
// of blocks opening on a term the question used; this one answers 129 with 97%.
func TestTheInformativeFloorCountsASubjectInOneSessionOfSeven(t *testing.T) {
	var texts []string
	for i := 0; i < 100; i++ {
		if i%7 == 0 {
			texts = append(texts, fmt.Sprintf("the kestrel timeout came up again in run %d", i))
			continue
		}
		texts = append(texts, fmt.Sprintf("pushed the branch and ran the suite again in run %d", i))
	}
	dir := nlIndex(t, texts...)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	rank, err := relevantMetasCounts(dir, m, []string{"p"}, []string{"kestrel"}, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rank.informative) == 0 {
		t.Fatal("the sessions holding the subject did not rank at all")
	}
	if rank.informative[0] < 1 {
		t.Fatalf("kestrel scored %.2f and was not counted: a subject in one session of seven is not filler",
			rank.idf["kestrel"])
	}
}
