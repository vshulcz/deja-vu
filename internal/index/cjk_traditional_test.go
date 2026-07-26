package index

import "testing"

func hasTerm(terms []string, want string) bool {
	for _, term := range terms {
		if term == want {
			return true
		}
	}
	return false
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
