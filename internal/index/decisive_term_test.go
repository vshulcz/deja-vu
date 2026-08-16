package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A question asked in plain words carries filler alongside the one word that
// identifies the answer. "How many bikes do I own?" ranks on many, bikes, own —
// and sessions holding the two filler words used to count as real matches while
// the single session that says bikes rode behind all of them, because the split
// between a real match and a weak one counted terms and never asked what they
// were worth.
//
// On the 1910-session day0 corpus that put the answer at rank 46 of 50, under
// wineries and invasive species. It is at 10 now.
func TestOneDecisiveWordOutranksTwoFillerOnes(t *testing.T) {
	// The filler words have to be common for this to be the real situation:
	// in a corpus of four everything is rare, idf collapses, and an earlier
	// tier answers before relevance is ever asked.
	var texts []string
	for i := 0; i < 40; i++ {
		texts = append(texts, "there are many ways to own one of these and many people own several")
	}
	answer := len(texts)
	texts = append(texts, "I keep three bikes in the garage and ride the blue to work")
	dir := nlIndex(t, texts...)

	// Straight at the relevance tier rather than through the ladder: on a corpus
	// this small an earlier tier answers first, and the ordering under test is
	// this one's.
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := relevanceSearch(dir, m, query.Options{Query: "How many bikes do I own?", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("a question whose rare word is in the corpus returned nothing")
	}
	want := string(rune('a' + answer))
	if got := result.Sessions[0].ID; got != want {
		rank := -1
		for i, s := range result.Sessions {
			if s.ID == want {
				rank = i + 1
			}
		}
		t.Errorf("the session holding the rare word ranked %d of %d, want it first (got %q first)",
			rank, len(result.Sessions), got)
	}
}

// The other half of the same rule, and the reason it is gated. Where the words
// a rare term stands alone against are not words at all, one surviving anchor
// is noise however rare it is, and the tier owes the query silence rather than
// its best guess.
func TestALoneRareWordAmongTyposStillSaysNothing(t *testing.T) {
	dir := nlIndex(t, "I keep three bikes in the garage and ride the blue to work")
	result, err := SearchDetailed(dir, search.Options{Query: "zzqx wwvv bikes", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 0 {
		t.Errorf("a query of two typos and one real word answered anyway: %d sessions", len(result.Sessions))
	}
}
