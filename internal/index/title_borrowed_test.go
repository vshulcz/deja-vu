package index

import "testing"

// A title the ingest borrowed from plumbing is not what a person typed. On an
// agent-heavy store that family is most of the titles, so a surface picking a
// phrase out of them has to skip it (#3714).
func TestTitleIsBorrowed(t *testing.T) {
	for _, s := range []string{
		"harness output: verify the shard rebalance report",
		"  harness output: something",
		"tool output: 42 files changed",
	} {
		if !TitleIsBorrowed(s) {
			t.Errorf("%q is a borrowed title", s)
		}
	}
	for _, s := range []string{
		"",
		"the shard rebalance keeps flapping",
		"output of the harness is fine to talk about",
	} {
		if TitleIsBorrowed(s) {
			t.Errorf("%q is a person's own title", s)
		}
	}
}
