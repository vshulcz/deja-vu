package search

import (
	"strings"
	"testing"
)

// The snippet path had its own two filters — a numbered-line check and a
// tool-dump check — and no notion of a listing, so `wc -l` output and a run of
// paths were quoted as the passage that explains a file. The digest side has
// had the rule all along; this is the same question asked on the other path.
//
// On a real store, 29 of the 60 most-worked-on files got a different blame
// answer once the rule was shared.
func TestASnippetDoesNotQuoteALineCount(t *testing.T) {
	text := "Counted the package:\n" +
		"128 domain/apiaccess/apiaccess_test.go 210 domain/apiaccess/apiaccess.go 51 domain/apiaccess/errors.go 70 domain/apiaccess/ledger.go 35 domain/apiaccess/ports.go\n" +
		"The split is fine, so I left the layout alone."
	got := proseForSnippet(text)
	if strings.Contains(got, "apiaccess_test.go") {
		t.Errorf("a wc -l dump was kept as prose:\n%s", got)
	}
	if !strings.Contains(got, "The split is fine") {
		t.Errorf("the sentence around it was dropped too:\n%s", got)
	}
}

// The filters it already had still do their work, and a sentence is still a
// sentence — the listing rule must not start eating prose on this path either.
func TestTheSnippetPathKeepsItsOtherFilters(t *testing.T) {
	got := proseForSnippet(
		"42:  func main() {\n" +
			"Raised pgbouncer default_pool_size to 40 and the timeouts stopped.\n" +
			"12 зелёных. Жду ревьюера перед мержем.")
	if strings.Contains(got, "func main") {
		t.Errorf("a numbered source line was kept:\n%s", got)
	}
	for _, want := range []string{"default_pool_size to 40", "Жду ревьюера"} {
		if !strings.Contains(got, want) {
			t.Errorf("prose was dropped, %q is gone:\n%s", want, got)
		}
	}
}

// Everything filtered leaves the old fallback standing: a snippet of the raw
// text beats an empty one.
func TestASnippetOfNothingButAListingIsStillSomething(t *testing.T) {
	got := proseForSnippet("main.go util.go parser.go writer.go reader.go index.go search.go digest.go")
	if strings.TrimSpace(got) == "" {
		t.Error("a record that is only a listing produced no snippet at all")
	}
}
