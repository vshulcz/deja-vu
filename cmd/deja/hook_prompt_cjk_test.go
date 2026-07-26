package main

import "testing"

// A CJK prompt is one field to FieldsFunc (no spaces) and techTerm rejects
// every rune above 127, so promptSearchTerms returned nothing at all and
// runHookPrompt bailed at the "fewer than two terms" guard: auto-recall could
// never fire for a Chinese, Japanese or Korean user.
func TestPromptSearchTermsCJK(t *testing.T) {
	cases := []struct {
		prompt  string
		wantAny []string
	}{
		{"韓國簽證點樣申請", []string{"韓國", "簽證"}},
		{"刷新令牌怎么实现", []string{"刷新", "令牌"}},
		{"パスワードの再設定", []string{"パス"}},
		{"полное совпадение по-русски", nil}, // Cyrillic path unchanged
	}
	for _, c := range cases {
		got := promptSearchTerms(c.prompt)
		if c.wantAny == nil {
			continue
		}
		if len(got) < 2 {
			t.Errorf("%q: got %v, want at least two terms so the hook can fire", c.prompt, got)
			continue
		}
		for _, want := range c.wantAny {
			var found bool
			for _, g := range got {
				if g == want {
					found = true
				}
			}
			if !found {
				t.Errorf("%q: term %q missing from %v", c.prompt, want, got)
			}
		}
	}
}

// Grammar must not become a term: 什么 / 在哪 style pairs are the CJK
// counterpart of a stop word, and RelevanceTerms already drops them, so the
// hook inherits that filtering for free.
func TestPromptSearchTermsCJKDropsGrammar(t *testing.T) {
	got := promptSearchTerms("我为什么不用 postgres 的索引")
	for _, junk := range []string{"什么", "在哪", "怎么"} {
		for _, g := range got {
			if g == junk {
				t.Errorf("grammar bigram %q became a search term: %v", junk, got)
			}
		}
	}
	var hasIndex bool
	for _, g := range got {
		if g == "索引" {
			hasIndex = true
		}
	}
	if !hasIndex {
		t.Errorf("content bigram 索引 missing from %v", got)
	}
}

// An ASCII prompt must behave exactly as before: the CJK branch is skipped.
func TestPromptSearchTermsASCIIUnchanged(t *testing.T) {
	got := promptSearchTerms("fix the authentication middleware timeout")
	for _, g := range got {
		if hasCJKRune(g) {
			t.Errorf("ASCII prompt produced a CJK term: %v", got)
		}
	}
	if len(got) == 0 {
		t.Errorf("ASCII prompt lost its terms: %v", got)
	}
}
