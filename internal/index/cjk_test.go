package index

import (
	cjkfold "github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// indexKeys calls cjkBigrams on every message body it sees, most of which are
// diffs, paths, stack traces and English prose with no CJK rune anywhere. This
// table pins the contract that lets those exit early: what comes back for a
// string with no CJK run is nothing, whatever else the bytes happen to be.
//
// It is also the guard on the byte scan itself. A scan that decided "ASCII" too
// eagerly would return nil for the CJK rows below; one that never fired would
// leave the ASCII rows unchanged and quietly cost what it always cost. Only the
// first failure mode can corrupt an index, and these rows catch it.
func TestCJKBigramsOnlyFiresOnCJK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"ascii prose", "the scheduler times out under load", nil},
		{"ascii symbols and digits", "GOFLAGS=-mod=mod exit(1) 200 OK", nil},
		{"cyrillic is not CJK", "очередь переполнена", nil},
		{"latin with accents", "café naïve", nil},
		{"invalid utf-8", "\xff\xfe\xfd", nil},
		{"single han keeps its unigram", "茶", []string{"茶"}},
		{"han run", "装订计数", []string{"装订", "订计", "计数"}},
		{"han among ascii", "abc装订def", []string{"装订"}},
		{"runs split by ascii", "装订 abc 计数", []string{"装订", "计数"}},
		{"repeated bigram appears once", "装订装订", []string{"装订", "订装"}},
		{"hiragana", "こんにちは", []string{"こん", "んに", "にち", "ちは"}},
		{"hangul", "한국어", []string{"한국", "국어"}},
		{"cyrillic next to han", "очередь装订", []string{"装订"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cjkfold.Bigrams(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("cjkfold.Bigrams(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("cjkfold.Bigrams(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// A body with no CJK in it should not allocate: nothing is built, so nothing
// needs a heap. This is the part a correctness table cannot see — the table
// passed before the early exit existed too, because the answer was always nil;
// what changed is the work done to arrive at it.
func TestCJKBigramsDoesNotAllocateOnNonCJK(t *testing.T) {
	body := strings.Repeat("the ingest worker leaks goroutines after a reload. ", 200)
	if allocs := testing.AllocsPerRun(50, func() { cjkfold.Bigrams(body) }); allocs != 0 {
		t.Errorf("cjkBigrams on a %d-byte ASCII body made %v allocations, want 0", len(body), allocs)
	}
}

func BenchmarkCJKBigramsASCII(b *testing.B) {
	body := strings.Repeat("the ingest worker leaks goroutines after a reload. ", 200)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for range b.N {
		cjkfold.Bigrams(body)
	}
}

func BenchmarkCJKBigramsCJK(b *testing.B) {
	body := strings.Repeat("摄取工作进程在重载之后泄漏协程。", 100)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for range b.N {
		cjkfold.Bigrams(body)
	}
}

// A Traditional corpus queried in Simplified (and the reverse) must match:
// CJK bigrams are raw codepoints, so without folding 距離 and 距离 are
// unrelated keys and recall is silently one-directional.
func TestCJKFoldCrossScriptSearch(t *testing.T) {
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
	mk("t1", "韓國旅遊簽證的申請進度") // Traditional content
	mk("s1", "刷新令牌怎么实现")    // Simplified content
	mk("j1", "パスワードの再設定")   // Japanese must be untouched
	mk("k1", "비밀번호 재설정")    // Korean must be untouched
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		query string
		want  string
		note  string
	}{
		{"簽證", "t1", "Traditional query, Traditional content"},
		{"签证", "t1", "Simplified query reaches Traditional content"},
		{"申請進度", "t1", "multi-bigram Traditional phrase"},
		{"申请进度", "t1", "multi-bigram Simplified query, Traditional content"},
		{"令牌", "s1", "Simplified query, Simplified content"},
		{"令牌", "s1", "unchanged for content that was already Simplified"},
		{"パスワード", "j1", "Katakana is not folded"},
		{"비밀번호", "k1", "Hangul is not folded"},
	}
	for _, c := range cases {
		got, err := Search(dir, query.Options{Query: c.query, All: true})
		if err != nil {
			t.Fatalf("%s: query %q: %v", c.note, c.query, err)
		}
		var found bool
		for _, s := range got {
			if s.ID == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: query %q did not reach %s (got %d sessions)", c.note, c.query, c.want, len(got))
		}
	}
}

// The fold is a pure Traditional-to-Simplified function: only runes with a
// single unambiguous simplification are listed, non-Han scripts pass through,
// and folding an already-Simplified string is a no-op.
func TestCJKFoldUnit(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"簽證", "签证"},
		{"刺蝟的距離", "刺猬的距离"},
		{"升學計劃", "升学计划"},
		{"签证", "签证"},                   // already Simplified
		{"hello world", "hello world"}, // Latin untouched
		{"パスワード", "パスワード"},             // Katakana untouched
		{"비밀번호", "비밀번호"},               // Hangul untouched
		{"混合 mixed 文字", "混合 mixed 文字"}, // already-Simplified mixed
	} {
		if got := cjkfold.String(c.in); got != c.want {
			t.Errorf("cjkfold.String(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Bigrams keep the script they were written in — the function-word filter
	// depends on that, because 係 folds to 系 which is content in 系統.
	if got := cjkfold.Bigrams("韓國簽證"); got[0] != "韓國" {
		t.Errorf("bigrams should stay unfolded, got %v", got)
	}
	// The keys built from them are what must agree across scripts.
	trad := indexKeys("韓國簽證")
	simp := indexKeys("韩国签证")
	tradBigrams := map[string]bool{}
	for _, k := range trad {
		if len([]rune(k)) == 3 { // "t" + two CJK runes
			tradBigrams[k] = true
		}
	}
	for _, k := range simp {
		if len([]rune(k)) != 3 {
			continue
		}
		if !tradBigrams[k] {
			t.Errorf("key %q from the Simplified spelling is absent for the Traditional one: %v vs %v", k, trad, simp)
		}
	}
}

// Folding changes posting keys, so an index written before it holds keys this
// build never queries. The version gate is what turns that into an automatic
// rebuild instead of silent zero recall for CJK.
func TestFoldRequiresAnIndexVersionOfItsOwn(t *testing.T) {
	if version < 16 {
		t.Fatalf("index version is %d: Traditional folding changes keys and needs its own version", version)
	}
}

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
		got, err := Search(dir, query.Options{Query: c.query, All: true})
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
	if got, _ := Search(dir, query.Options{Query: "c装", All: true}); len(got) > 0 {
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
	got, err := Search(dir, query.Options{Query: q, All: true})
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
	got, err := SearchDetailed(dir, query.Options{Query: "刷新令牌是什么时候开始失效的？", All: true})
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
	terms := RelevanceTerms("复旦大学在哪个城市？")
	for _, junk := range []string{"在哪", "哪个"} {
		if slices.Contains(terms, junk) {
			t.Errorf("function bigram %q survived as a query term: %v", junk, terms)
		}
	}
	for _, want := range []string{"复旦", "大学", "城市"} {
		if !slices.Contains(terms, want) {
			t.Errorf("content bigram %q was dropped: %v", want, terms)
		}
	}
	// The rule is "every rune is a function rune", not "any": these are real
	// words built from characters that are function runes on their own.
	if got := RelevanceTerms("中国有多少个人"); !slices.Contains(got, "中国") || !slices.Contains(got, "个人") {
		t.Errorf("real words dropped from %q: %v", "中国有多少个人", got)
	}
	if got := RelevanceTerms("目的是什么"); !slices.Contains(got, "目的") {
		t.Errorf("real word 目的 dropped: %v", got)
	}
	// Latin is untouched — the filter requires CJK runes.
	if got := RelevanceTerms("what is the database migration"); !slices.Contains(got, "database") {
		t.Errorf("ASCII query lost content terms: %v", got)
	}
}

func TestCJKMessageTermsAndCrossScriptBridge(t *testing.T) {
	terms := RelevanceTerms("数据库连接池死锁")
	mt := newMessageTerms(terms, nil, false)
	if !mt.unspaced {
		t.Fatalf("expected mt.unspaced for CJK terms %v", terms)
	}
	// Exact contiguous match scores above scattered terms via mt.window proximity.
	tight := mt.best(model.Session{Messages: []model.Message{{Role: "assistant", Text: "今天定位并修复了数据库连接池死锁的故障"}}})
	scattered := mt.best(model.Session{Messages: []model.Message{{Role: "assistant", Text: "今天看了数据库配置，后来调整了连接池参数，顺便排查了死锁"}}})
	if tight <= float64(len(terms)) || tight <= scattered {
		t.Fatalf("expected CJK proximity lift: tight=%v scattered=%v len(terms)=%d", tight, scattered, len(terms))
	}
	// Traditional Chinese message matches Simplified query terms.
	tradScore := mt.best(model.Session{Messages: []model.Message{{Role: "assistant", Text: "今天定位並修復了數據庫連接池死鎖的故障"}}})
	if tradScore != tight {
		t.Fatalf("expected Traditional Chinese message to score identically: trad=%v tight=%v", tradScore, tight)
	}

	// Cross-script bridge connects Chinese/Russian query terms to English sessions.
	dir := cjkIndex(t, map[string]string{
		"s_en":    "Fixed the database deadlock in the connection pool by bounding transaction retries.",
		"s_other": "Updated CSS colors for the marketing landing page.",
	})
	for _, q := range []string{"为什么数据库死锁", "почему дедлок базы"} {
		got, err := SearchDetailed(dir, query.Options{Query: q, All: true})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(got.Sessions) == 0 || got.Sessions[0].ID != "s_en" {
			t.Fatalf("cross-script query %q failed to bridge to English session s_en: got %+v", q, got.Sessions)
		}
	}
}

func hasTerm(terms []string, want string) bool {
	return slices.Contains(terms, want)
}

// cjkFunctionRunes listed only Simplified Mandarin forms, so the same closed
// class written in Traditional characters arrived as content: a Traditional
// writer asking 復旦大學在哪個城市 got 在哪 and 哪個 weighted like the entity
// they were asking about, which is exactly what
// TestCJKFunctionBigramsAreNotQueryTerms prevents for Simplified input.
func TestCJKFunctionBigramsTraditional(t *testing.T) {
	terms := RelevanceTerms("復旦大學在哪個城市？")
	for _, junk := range []string{"在哪", "哪個"} {
		if hasTerm(terms, junk) {
			t.Errorf("Traditional function bigram %q survived as a query term: %v", junk, terms)
		}
	}
	for _, want := range []string{"復旦", "大學", "城市"} {
		if !hasTerm(terms, want) {
			t.Errorf("content bigram %q was dropped: %v", want, terms)
		}
	}
	// The "every rune" rule must keep real words whose characters are function
	// runes on their own, exactly as it does for Simplified.
	if got := RelevanceTerms("中國有多少個人"); !hasTerm(got, "中國") || !hasTerm(got, "個人") {
		t.Errorf("real words dropped from 中國有多少個人: %v", got)
	}
	if got := RelevanceTerms("目的是什麼"); !hasTerm(got, "目的") {
		t.Errorf("real word 目的 dropped: %v", got)
	}
	for _, pair := range []struct{ query, want string }{
		{"這個關係好緊要", "關係"},
		{"樣本數量", "樣本"},
		{"重點係邊個", "重點"},
	} {
		if got := RelevanceTerms(pair.query); !hasTerm(got, pair.want) {
			t.Errorf("real word %q dropped from %q: %v", pair.want, pair.query, got)
		}
	}
}

// Cantonese writes its own closed class — 嘅 for 的, 係 for 是, 喺 for 在,
// 唔 for 不, 咩/乜 for 什麼, 點 for 怎, 哋 for 們, 嗰 for 那, 啲 for 些 — none
// of which were listed, so a Cantonese question contributed its whole grammar
// to the relevance tier.
func TestCJKFunctionBigramsCantonese(t *testing.T) {
	terms := RelevanceTerms("我點解唔用 postgres 嘅索引")
	if hasTerm(terms, "我點") {
		t.Errorf("Cantonese grammar bigram 我點 survived as a query term: %v", terms)
	}
	if !hasTerm(terms, "postgres") {
		t.Errorf("the entity being asked about was dropped: %v", terms)
	}
	if !hasTerm(terms, "索引") {
		t.Errorf("content bigram 索引 was dropped: %v", terms)
	}

	// Bigrams that are grammar end to end.
	for _, junk := range []string{"係咁", "咁樣", "喺呢", "嗰啲", "我哋", "呢啲"} {
		if got := RelevanceTerms(junk + "做"); hasTerm(got, junk) {
			t.Errorf("Cantonese grammar bigram %q survived: %v", junk, got)
		}
	}

	// Content words built from characters that are function runes on their own
	// must survive, the same guarantee the Simplified list already gives.
	for _, pair := range []struct{ query, want string }{
		{"睇下個人資料", "個人"},
		{"關係好緊要", "關係"},
		{"重點係邊個", "重點"},
		{"冇錢用", "冇錢"},
	} {
		if got := RelevanceTerms(pair.query); !hasTerm(got, pair.want) {
			t.Errorf("real word %q dropped from %q: %v", pair.want, pair.query, got)
		}
	}
}

// The point of the patch is parity: a question asked in Traditional or in
// Cantonese should reduce to the same shape of terms as its Simplified
// counterpart. Boundary bigrams that pair a particle with a content character
// (的索 in Simplified, 嘅索 in Cantonese) survive in both — that is inherent to
// bigram tokenization, not a script-specific defect.
func TestCJKFunctionBigramParityAcrossScripts(t *testing.T) {
	countGrammar := func(q string, grammar []string) int {
		terms := RelevanceTerms(q)
		n := 0
		for _, g := range grammar {
			if hasTerm(terms, g) {
				n++
			}
		}
		return n
	}
	// Pure-grammar bigrams of the question form "why don't I use X".
	if simp := countGrammar("我为什么不用 postgres 的索引", []string{"我为", "为什", "什么"}); simp != 0 {
		t.Fatalf("baseline changed: Simplified grammar bigrams leaked: %d", simp)
	}
	if yue := countGrammar("我點解唔用 postgres 嘅索引", []string{"我點"}); yue != 0 {
		t.Errorf("Cantonese grammar bigrams leaked where Simplified's did not: %d", yue)
	}
	if trad := countGrammar("復旦大學在哪個城市？", []string{"在哪", "哪個"}); trad != 0 {
		t.Errorf("Traditional grammar bigrams leaked where Simplified's did not: %d", trad)
	}
}
