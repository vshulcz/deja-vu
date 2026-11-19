package stats

import (
	"reflect"
	"testing"
)

// The new shape counts only with the session id: "déjà vu" alone is an
// ordinary phrase, and a session about the tool says it without crediting
// anything. The old shape still counts, because the transcripts that say it
// are still on disk.
func TestCreditedAloudNeedsTheIDForTheNewShape(t *testing.T) {
	cases := map[string]bool{
		"déjà vu: you asked this on Aug 26 in opencode; it was settled as a vault lease (deja:ses_fc145)": true,
		"Déjà vu: \"why did we drop the trigram index\" — size vs traffic (codex, May 1, deja:cx0052)":    true,
		"deja-vu recalled: the webhook retry cap — reusing it.":                                           true,
		"the déjà vu line is rate-limited per session":                                                    false,
		"deja:cx0052 is the session to read":                                                              false,
		"":                                                                                                false,
	}
	for text, want := range cases {
		if got := CreditedAloud(text); got != want {
			t.Errorf("CreditedAloud(%q) = %v, want %v", text, got, want)
		}
	}
}

// Only an id on the credit line counts, once, and the old shape names none.
func TestCreditedRefsTakesTheIDsOnTheCreditLine(t *testing.T) {
	cases := map[string][]string{
		"déjà vu: the lease fix (claude, Oct 9, deja:65dc9f50-3f6) — reusing it.":                  {"65dc9f50-3f6"},
		"deja vu: unaccented credit line (claude, Oct 9, deja:65dc9f50-3f6) — reusing it.":         {"65dc9f50-3f6"},
		"Déjà vu: two at once (deja:65dc9f50-3f6, deja:ses_fc145aa)\nDéjà vu: (deja:65dc9f50-3f6)": {"65dc9f50-3f6", "ses_fc145aa"},
		"see deja:65dc9f50-3f6 for the lease\ndéjà vu: nothing named here":                         nil,
		"déjà vu: too short to be an id (deja:abc)":                                                nil,
		"deja-vu recalled: the webhook retry cap — reusing it.":                                    nil,
		"": nil,
	}
	for text, want := range cases {
		if got := CreditedRefs(text); !reflect.DeepEqual(got, want) {
			t.Errorf("CreditedRefs(%q) = %q, want %q", text, got, want)
		}
	}
	if !CreditedAloud("deja vu: unaccented credit line (deja:65dc9f50-3f6)") {
		t.Error("CreditedAloud should accept unaccented 'deja vu:' credit lines")
	}
	if !AskedByAPerson("为什么数据库连接池在重连后超时了") || !AskedByAPerson("这个函数可以缓存结果吗") {
		t.Error("AskedByAPerson should accept Chinese questions without a question mark")
	}
	if got, want := questionStemFor("為什麼資料庫連接池超時"), questionStemFor("为什么资料库连接池超时"); got != want {
		t.Errorf("questionStemFor should fold Traditional Chinese to Simplified Chinese: got %q, want %q", got, want)
	}
}
