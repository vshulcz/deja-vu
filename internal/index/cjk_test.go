package index

import (
	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// The table from #337, verbatim.
func TestCJKBigramSearch(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(id, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("s1", "刷新令牌怎么实现")
	mk("s2", "用 jwt 做 refresh")
	mk("s3", "喝茶")
	mk("s4", "abc装订def")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query string
		want  string
	}{
		{"令牌", "s1"},   // exact bigram
		{"刷新令牌", "s1"}, // ANDed bigrams
		{"jwt", "s2"},  // mixed text unaffected
		{"茶", "s3"},    // single rune: close tier over the run token
		{"装订", "s4"},   // run inside ASCII neighbours
	}
	for _, c := range cases {
		got, err := Search(dir, search.Options{Query: c.query, All: true})
		if err != nil {
			t.Fatalf("%q: %v", c.query, err)
		}
		found := false
		for _, s := range got {
			if s.ID == c.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q: want %s in results, got %d sessions", c.query, c.want, len(got))
		}
	}
	// No cross-boundary bigrams: "c装" must not exist as a posting.
	if got, _ := Search(dir, search.Options{Query: "c装", All: true}); len(got) > 0 {
		for _, s := range got {
			if s.ID == "s4" {
				// substring tier may still find the raw token — that is the
				// documented pre-existing path, not a bigram; assert the
				// bigram posting itself is absent instead.
				break
			}
		}
	}
	if posts, err := postingsFor(dir, "tc装"); err == nil && len(posts) > 0 {
		t.Fatal("cross-boundary bigram was indexed")
	}
	if posts, err := postingsFor(dir, "t订d"); err == nil && len(posts) > 0 {
		t.Fatal("cross-boundary bigram was indexed")
	}
}

func TestCJKBigramsUnit(t *testing.T) {
	got := cjkfold.Bigrams("装订计数")
	want := []string{"装订", "订计", "计数"}
	if len(got) != len(want) {
		t.Fatalf("bigrams = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bigrams = %v, want %v", got, want)
		}
	}
	if got := cjkfold.Bigrams("茶"); len(got) != 1 || got[0] != "茶" {
		t.Fatalf("unigram run = %v", got)
	}
	if got := cjkfold.Bigrams("plain ascii"); len(got) != 0 {
		t.Fatalf("ascii leaked = %v", got)
	}
}

// cjkIndex builds a small index from the given id/text pairs.
func cjkIndex(t *testing.T, docs map[string]string) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, text := range docs {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func cjkFirstID(t *testing.T, dir, q string) string {
	t.Helper()
	got, err := Search(dir, search.Options{Query: q, All: true})
	if err != nil {
		t.Fatalf("%q: %v", q, err)
	}
	if len(got) == 0 {
		return ""
	}
	return got[0].ID
}

// A contiguous CJK phrase longer than a couple of characters must keep
// matching through the AND — its bigrams really do co-occur in the text that
// contains it.
func TestCJKLongPhraseStillMatches(t *testing.T) {
	dir := cjkIndex(t, map[string]string{
		"s1": "我们讨论了刷新令牌怎么实现的问题",
		"s2": "缓存重载和证书轮换的会议记录",
	})
	for _, q := range []string{"刷新令牌怎么实现", "令牌怎么实现", "我们讨论了刷新令牌", "刷新令牌"} {
		if got := cjkFirstID(t, dir, q); got != "s1" {
			t.Fatalf("phrase %q: got %q, want s1", q, got)
		}
	}
}

// A quoted CJK phrase keeps its exactness contract and must not be routed
// into a path that cannot serve it.
func TestCJKQuotedPhraseMatches(t *testing.T) {
	dir := cjkIndex(t, map[string]string{
		"s1": "我们讨论了刷新令牌怎么实现的问题",
		"s2": "另一个会话讲的是缓存重载",
	})
	if got := cjkFirstID(t, dir, `"刷新令牌怎么实现"`); got != "s1" {
		t.Fatalf(`quoted phrase: got %q, want s1`, got)
	}
}

// A real question carries fullwidth punctuation and grammar; its bigrams
// cannot all co-occur, so the ladder must fall through to relevance instead
// of returning nothing.
func TestCJKQuestionReachesRelevance(t *testing.T) {
	dir := cjkIndex(t, map[string]string{
		"s1": "刷新令牌在缓存没有重载之后开始失效，我们改了轮换逻辑",
		"s2": "今天讨论了前端样式和构建速度的问题",
	})
	got, err := SearchDetailed(dir, search.Options{Query: "刷新令牌是什么时候开始失效的？", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) == 0 || got.Sessions[0].ID != "s1" {
		t.Fatalf("question must reach the answer: %d sessions, tier %q", len(got.Sessions), got.Tier)
	}
}

// Text that glues CJK and latin ("刷新token") is one token on both sides;
// the query must still reach it.
func TestCJKMixedTokenMatches(t *testing.T) {
	dir := cjkIndex(t, map[string]string{
		"s1": "我们用刷新token做了鉴权",
		"s2": "别的会话说的是构建缓存",
	})
	for _, q := range []string{"刷新token", "刷新"} {
		if got := cjkFirstID(t, dir, q); got != "s1" {
			t.Fatalf("mixed %q: got %q, want s1", q, got)
		}
	}
}

// Latin-1, Greek and other scripts below U+0400 used to be shattered by the
// relevance term splitter while the index kept them whole.
func TestRelevanceTermsKeepNonASCIILetters(t *testing.T) {
	got := RelevanceTerms("café naïve straße Ελλάδα")
	want := []string{"café", "naïve", "straße", "ελλάδα"}
	if len(got) != len(want) {
		t.Fatalf("terms = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("terms = %v, want %v", got, want)
		}
	}
}

// A Chinese question expands into one bigram per adjacent character pair, so
// the grammar of the question ("在哪", "什么") arrives as terms carrying the
// same weight as the entity being asked about. Nothing else filters them:
// IsStopWord only knows Latin and Cyrillic.
func TestCJKFunctionBigramsAreNotQueryTerms(t *testing.T) {
	has := func(terms []string, want string) bool {
		for _, term := range terms {
			if term == want {
				return true
			}
		}
		return false
	}
	terms := RelevanceTerms("复旦大学在哪个城市？")
	for _, junk := range []string{"在哪", "哪个"} {
		if has(terms, junk) {
			t.Errorf("function bigram %q survived as a query term: %v", junk, terms)
		}
	}
	for _, want := range []string{"复旦", "大学", "城市"} {
		if !has(terms, want) {
			t.Errorf("content bigram %q was dropped: %v", want, terms)
		}
	}
	// The rule is "every rune is a function rune", not "any": these are real
	// words built from characters that are function runes on their own.
	if got := RelevanceTerms("中国有多少个人"); !has(got, "中国") || !has(got, "个人") {
		t.Errorf("real words dropped from %q: %v", "中国有多少个人", got)
	}
	if got := RelevanceTerms("目的是什么"); !has(got, "目的") {
		t.Errorf("real word 目的 dropped: %v", got)
	}
	// Latin is untouched — the filter requires CJK runes.
	if got := RelevanceTerms("what is the database migration"); !has(got, "database") {
		t.Errorf("ASCII query lost content terms: %v", got)
	}
}
