package index

import (
	"strings"
	"testing"

	query "github.com/vshulcz/deja-vu/internal/query"
)

// A pair of function runes is the CJK counterpart of a stop word. The query
// side has dropped it from the term list since it was written; the index kept
// storing it, so the postings carried the most frequent pairs in the language
// for nothing (#492).
func TestGrammarBigramsEarnNoPosting(t *testing.T) {
	var got []string
	cjkIndexKeys("这是什么问题", func(tok string) { got = append(got, tok) })
	joined := strings.Join(got, " ")
	for _, grammar := range []string{"t这是", "t是什", "t什么"} {
		if strings.Contains(joined, grammar) {
			t.Errorf("grammar bigram %q was stored: %v", grammar, got)
		}
	}
	// And the content pairs are still there, including the ones that straddle
	// grammar and content: 么问 is half a function rune and half a word.
	for _, content := range []string{"t么问", "t问题"} {
		if !strings.Contains(joined, content) {
			t.Errorf("content bigram %q was dropped: %v", content, got)
		}
	}
}

// A single function rune that also carries meaning keeps its pairs: 中 and 个
// are in the closed class, and 中国 and 个人 are words.
func TestAFunctionRuneBesideAContentRuneKeepsItsBigram(t *testing.T) {
	var got []string
	cjkIndexKeys("中国个人", func(tok string) { got = append(got, tok) })
	joined := strings.Join(got, " ")
	for _, want := range []string{"t中国", "t国个", "t个人"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q was dropped: %v", want, got)
		}
	}
}

// The two sides ask for the same thing: what the index no longer stores, the
// query no longer puts in the AND.
func TestTheQuerySideDropsTheSameBigrams(t *testing.T) {
	keys := queryKeys("这是什么问题")
	for _, k := range keys {
		if query.CJKFunctionBigram(strings.TrimPrefix(k, "t")) {
			t.Errorf("the query asks for a grammar bigram the index does not hold: %q of %v", k, keys)
		}
	}
	if len(keys) == 0 {
		t.Error("the query lost every key")
	}
}

// A sentence written entirely in the closed class is grammar with no subject;
// it must not take the whole store with it.
func TestAGrammarOnlySentenceFindsNothingInParticular(t *testing.T) {
	dir := seedOneWord(t, "这是什么问题")
	res, err := SearchDetailed(dir, query.Options{Query: "是的了", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 0 {
		t.Errorf("a grammar-only query returned %d sessions", len(res.Sessions))
	}
}
