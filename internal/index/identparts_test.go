package index

import (
	"reflect"
	"sort"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestIdentifierParts(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"call getUserById now", []string{"get", "user"}}, // "by","id" < 3 runes
		{"refresh_token_rotation broke", []string{"refresh", "token", "rotation"}},
		{"JSONDataReader failed", []string{"json", "data", "reader"}},
		{"kebab-case-slug", []string{"kebab", "case", "slug"}},
		{"plain words only here", nil},
		{"short a_b", nil},
	}
	for _, tc := range cases {
		got := identifierParts(tc.in)
		sort.Strings(got)
		want := append([]string(nil), tc.want...)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("identifierParts(%q) = %v, want %v", tc.in, got, want)
		}
	}
}

func TestIdentifierSplitRetrieval(t *testing.T) {
	dir := nlIndex(t,
		"the bug is in getUserProfile handler",
		"unrelated session about docker",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "user profile", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != "a" {
		t.Fatalf("split retrieval missed camelCase: %+v", result.Sessions)
	}
}

func TestRetrievalKeysFetchesMoreCandidates(t *testing.T) {
	keys := []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9"}
	got := retrievalKeys(keys)
	if len(got) != 8 {
		t.Fatalf("retrievalKeys cap = %d, want 8", len(got))
	}
	short := []string{"a", "b"}
	if len(retrievalKeys(short)) != 2 {
		t.Fatal("short key lists must pass through")
	}
}

func TestWiderKeyCapShrinksCandidateSet(t *testing.T) {
	// 40 sessions share five long common words; one also has a rare short
	// token. The old 3-longest cap fetched only common postings (40
	// candidates for the scan); fetching the rare token's postings too
	// collapses the AND to 1 before any record is read.
	texts := make([]string, 0, 40)
	for i := 0; i < 39; i++ {
		texts = append(texts, "performance regression investigation deployment pipeline filler")
	}
	texts = append(texts, "performance regression investigation deployment pipeline zzq")
	dir := nlIndex(t, texts...)

	keys := queryKeys("performance regression investigation deployment pipeline zzq")
	oldSel := keys
	if len(oldSel) > 3 {
		oldSel = oldSel[:3]
	}
	oldPosts, err := intersectPostings(dir, oldSel)
	if err != nil {
		t.Fatal(err)
	}
	newPosts, err := intersectPostings(dir, retrievalKeys(keys))
	if err != nil {
		t.Fatal(err)
	}
	if len(oldPosts) < 30 {
		t.Fatalf("fixture broken: old-cap candidates = %d, want ~40", len(oldPosts))
	}
	if len(newPosts) >= len(oldPosts)/10 {
		t.Fatalf("wide cap did not narrow candidates: old=%d new=%d", len(oldPosts), len(newPosts))
	}
}
