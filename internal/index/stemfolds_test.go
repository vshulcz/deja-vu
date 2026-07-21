package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestDevSynonymFoldBridgesAbbreviations(t *testing.T) {
	dir := nlIndex(t,
		"the authentication middleware rejects rotated tokens",
		"kubernetes ingress drops websocket upgrades",
		"unrelated docker session filler",
	)
	// auth->authentication rides the substring tier (prefix); the synonym
	// table exists for pairs with no substring bridge, like k8s->kubernetes.
	for query, wantID := range map[string]string{
		"auth middleware rejects": "a",
		"k8s ingress websocket":   "b",
	} {
		result, err := SearchDetailed(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) != 1 || result.Sessions[0].ID != wantID {
			t.Fatalf("%q: sessions=%+v", query, result.Sessions)
		}
	}
	k8s, err := SearchDetailed(dir, search.Options{Query: "k8s ingress websocket", All: true})
	if err != nil || !k8s.Stemmed {
		t.Fatalf("k8s must resolve via the stem tier: %+v %v", k8s, err)
	}
}

func TestDevSynonymNeverTouchesExactTier(t *testing.T) {
	dir := nlIndex(t,
		"auth service config drift",
		"authentication deep dive",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "auth service", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stemmed || len(result.Sessions) != 1 || result.Sessions[0].ID != "a" {
		t.Fatalf("exact tier polluted: %+v", result)
	}
}

func TestCyrillicSuffixFold(t *testing.T) {
	dir := nlIndex(t,
		"миграции таблиц падают на проде после отката",
		"unrelated english session",
	)
	for _, q := range []string{"миграция падает", "миграцию откат"} {
		result, err := SearchDetailed(dir, search.Options{Query: q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) != 1 || result.Sessions[0].ID != "a" {
			t.Fatalf("%q: sessions=%+v variants=%v", q, result.Sessions, result.Variants)
		}
	}
}
