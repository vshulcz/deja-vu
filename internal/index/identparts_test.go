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
